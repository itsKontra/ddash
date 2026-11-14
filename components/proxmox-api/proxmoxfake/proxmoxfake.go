// Package proxmoxfake is a stateful fake of the Proxmox VE API for
// proxmox-api's power Actions. It keeps each Workload's status, refuses a
// power request where the token lacks VM.PowerMgmt (as Proxmox's ACL
// does), runs each power request as a task that ends after a number of
// status reads, can list a Workload's old status for a while after its
// task ended (as /cluster/resources does until pvestatd refreshes it),
// keeps each Workload's snapshots (seeded from a scenario's
// snapshot.json) and runs their create and delete as tasks, fails a
// snapshot on a Workload whose feature.json says the storage can't, runs
// vzdump as a task, and records every request in order; anything else is
// served from a scenario's fixtures (fixture.Scenario). proxmox-api's
// tests and fakelab serve it.
package proxmoxfake

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/itsKontra/ddash/sdk/fixture"
)

const (
	api            = "/api2/json"
	resourcesPath  = api + "/cluster/resources"
	permissionPath = api + "/access/permissions"
)

// DefaultTaskPolls is how many status reads a task takes to end unless
// Options or SetTaskPolls say otherwise.
const DefaultTaskPolls = 2

// Options shape a Proxmox.
type Options struct {
	// Miss is told about a request no fixture answers (fixture.Scenario).
	Miss func(request string, err error)
	// TaskPolls is how many status reads a task takes to end: it reports
	// stopped on the TaskPolls-th read. 0 is DefaultTaskPolls.
	TaskPolls int
	// StatusLag is how many reads of /cluster/resources still list a
	// Workload as it was before a task that ended OK changed it (Proxmox
	// refreshes that list only every ~10 s). 0 lists the change at once.
	StatusLag int
}

// Proxmox is the fake, an http.Handler.
type Proxmox struct {
	fixtures http.Handler

	mu        sync.Mutex
	guests    []map[string]any
	perms     map[string]map[string]int
	polls     int
	lag       int
	stale     map[int]*staleGuest
	failNext  string
	tasks     map[string]*task
	seq       int
	log       []string
	startedAt time.Time
	// snapshots are each Workload's, by VMID, oldest first.
	snapshots map[int][]Snapshot
	// noSnapshot are the Workloads whose storage can't snapshot.
	noSnapshot map[int]bool
	backups    []Backup
}

// Snapshot is one snapshot of a Workload, as the fake keeps it.
type Snapshot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SnapTime    int64  `json:"snaptime"`
	Parent      string `json:"parent,omitempty"`
}

// Backup is a vzdump the fake ran.
type Backup struct {
	VMID    int
	Storage string
	Mode    string
	UPID    string
}

// staleGuest is a Workload as /cluster/resources still lists it for its
// next reads after a task changed it.
type staleGuest struct {
	guest map[string]any
	reads int
}

type task struct {
	upid, node, typ, id, exit string
	vmid                      int
	started                   time.Time
	reads, polls              int
	done                      bool
	// effect applies the task's change to its Workload when it ends OK.
	effect func(g map[string]any)
}

// New serves the scenario in dir: its cluster/resources.json and
// access/permissions.json (each else its sibling baseline's) are the
// starting state.
func New(dir string, opts Options) (*Proxmox, error) {
	p := &Proxmox{
		fixtures:  fixture.Scenario(dir, opts.Miss),
		polls:     opts.TaskPolls,
		lag:       opts.StatusLag,
		stale:     map[int]*staleGuest{},
		tasks:     map[string]*task{},
		startedAt: time.Now(),
	}
	if p.polls <= 0 {
		p.polls = DefaultTaskPolls
	}
	if err := seed(dir, resourcesPath, &p.guests); err != nil {
		return nil, err
	}
	if err := seed(dir, permissionPath, &p.perms); err != nil {
		return nil, err
	}
	p.snapshots, p.noSnapshot = map[int][]Snapshot{}, map[int]bool{}
	for _, g := range p.guests {
		id, _ := g["vmid"].(float64)
		node, _ := g["node"].(string)
		kind, _ := g["type"].(string)
		base := fmt.Sprintf("%s/nodes/%s/%s/%d", api, node, kind, int(id))
		var snaps []Snapshot
		if seed(dir, base+"/snapshot", &snaps) == nil {
			snaps = slices.DeleteFunc(snaps, func(s Snapshot) bool { return s.Name == "current" })
			p.snapshots[int(id)] = snaps
		}
		var feature struct {
			HasFeature int `json:"hasFeature"`
		}
		if seed(dir, base+"/feature", &feature) == nil && feature.HasFeature == 0 {
			p.noSnapshot[int(id)] = true
		}
	}
	return p, nil
}

// Snapshots returns a Workload's snapshots now, oldest first.
func (p *Proxmox) Snapshots(vmid int) []Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.snapshots[vmid])
}

// SetSnapshots replaces a Workload's snapshots.
func (p *Proxmox) SetSnapshots(vmid int, snaps []Snapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshots[vmid] = slices.Clone(snaps)
}

// SetSnapshotSupport says whether a Workload's storage can snapshot: a
// snapshot task on one that can't fails as on LVM.
func (p *Proxmox) SetSnapshotSupport(vmid int, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.noSnapshot[vmid] = !ok
}

// Backups returns every vzdump the fake ran, in order.
func (p *Proxmox) Backups() []Backup {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.backups)
}

// target splits /nodes/{node}/{lxc|qemu}/{vmid}/… and finds its Workload.
// Call with p.mu held.
func (p *Proxmox) target(path string) (node, kind string, vmid int, rest []string, err error) {
	parts := strings.Split(strings.TrimPrefix(path, api+"/"), "/")
	if len(parts) < 5 || parts[0] != "nodes" {
		return "", "", 0, nil, fmt.Errorf("no such method")
	}
	node, kind = parts[1], parts[2]
	if vmid, err = strconv.Atoi(parts[3]); err != nil {
		return "", "", 0, nil, fmt.Errorf("bad vmid")
	}
	g := p.guest(vmid)
	if g == nil || g["node"] != node || g["type"] != kind {
		return "", "", 0, nil, fmt.Errorf("Configuration file 'nodes/%s/%s/%d.conf' does not exist", node, kind, vmid) //nolint:staticcheck // Proxmox's own words
	}
	return node, kind, vmid, parts[4:], nil
}

// snapshot answers GET (list), POST (create) on
// /nodes/{node}/{kind}/{vmid}/snapshot and DELETE on …/snapshot/{name},
// each write as a task.
func (p *Proxmox) snapshot(w http.ResponseWriter, method, path string, params map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	node, kind, vmid, rest, err := p.target(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	prefix := "vz"
	if kind == "qemu" {
		prefix = "qm"
	}
	switch {
	case method == http.MethodGet && len(rest) == 1:
		list := []map[string]any{}
		parent := ""
		for _, s := range p.snapshots[vmid] {
			e := map[string]any{"name": s.Name, "description": s.Description, "snaptime": s.SnapTime}
			if s.Parent != "" {
				e["parent"] = s.Parent
			}
			list = append(list, e)
			parent = s.Name
		}
		current := map[string]any{"name": "current", "description": "You are here!", "running": 1}
		if parent != "" {
			current["parent"] = parent
		}
		b, err := json.Marshal(append(list, current))
		reply(w, b, err)
	case method == http.MethodPost && len(rest) == 1:
		if !p.privileged(vmid, "VM.Snapshot") {
			http.Error(w, fmt.Sprintf("Permission check failed (/vms/%d, VM.Snapshot)", vmid), http.StatusForbidden)
			return
		}
		name, _ := params["snapname"].(string)
		desc, _ := params["description"].(string)
		if slices.ContainsFunc(p.snapshots[vmid], func(s Snapshot) bool { return s.Name == name }) {
			http.Error(w, fmt.Sprintf("snapshot name '%s' already used", name), http.StatusInternalServerError)
			return
		}
		t := p.newTask(node, prefix+"snapshot", vmid, func(map[string]any) {
			parent := ""
			if snaps := p.snapshots[vmid]; len(snaps) > 0 {
				parent = snaps[len(snaps)-1].Name
			}
			p.snapshots[vmid] = append(p.snapshots[vmid], Snapshot{Name: name, Description: desc + "\n", SnapTime: p.now().Unix(), Parent: parent})
		})
		if p.noSnapshot[vmid] {
			t.exit = "snapshot feature is not available"
		}
		b, err := json.Marshal(t.upid)
		reply(w, b, err)
	case method == http.MethodDelete && len(rest) == 2:
		if !p.privileged(vmid, "VM.Snapshot") {
			http.Error(w, fmt.Sprintf("Permission check failed (/vms/%d, VM.Snapshot)", vmid), http.StatusForbidden)
			return
		}
		name := rest[1]
		if !slices.ContainsFunc(p.snapshots[vmid], func(s Snapshot) bool { return s.Name == name }) {
			http.Error(w, fmt.Sprintf("snapshot '%s' does not exist", name), http.StatusInternalServerError)
			return
		}
		t := p.newTask(node, prefix+"delsnapshot", vmid, func(map[string]any) {
			p.snapshots[vmid] = slices.DeleteFunc(p.snapshots[vmid], func(s Snapshot) bool { return s.Name == name })
		})
		b, err := json.Marshal(t.upid)
		reply(w, b, err)
	default:
		http.Error(w, "no such method", http.StatusNotImplemented)
	}
}

// now is the fake's clock for snapshot times.
func (p *Proxmox) now() time.Time { return time.Now() }

// feature answers GET /nodes/{node}/{kind}/{vmid}/feature?feature=snapshot.
func (p *Proxmox) feature(w http.ResponseWriter, path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _, vmid, _, err := p.target(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	has := 1
	if p.noSnapshot[vmid] {
		has = 0
	}
	reply(w, fmt.Appendf(nil, `{"hasFeature":%d}`, has), nil)
}

// vzdump answers POST /nodes/{node}/vzdump with a task.
func (p *Proxmox) vzdump(w http.ResponseWriter, path string, params map[string]any) {
	node := strings.TrimSuffix(strings.TrimPrefix(path, api+"/nodes/"), "/vzdump")
	vmid := 0
	switch v := params["vmid"].(type) {
	case float64:
		vmid = int(v)
	case string:
		vmid, _ = strconv.Atoi(v)
	}
	storage, _ := params["storage"].(string)
	mode, _ := params["mode"].(string)
	p.mu.Lock()
	defer p.mu.Unlock()
	if g := p.guest(vmid); g == nil || g["node"] != node {
		http.Error(w, fmt.Sprintf("guest %d not found on node %s", vmid, node), http.StatusInternalServerError)
		return
	}
	if !p.privileged(vmid, "VM.Backup") {
		http.Error(w, fmt.Sprintf("Permission check failed (/vms/%d, VM.Backup)", vmid), http.StatusForbidden)
		return
	}
	if storage == "" {
		http.Error(w, "storage: property is missing", http.StatusBadRequest)
		return
	}
	t := p.newTask(node, "vzdump", vmid, func(map[string]any) {})
	p.backups = append(p.backups, Backup{VMID: vmid, Storage: storage, Mode: mode, UPID: t.upid})
	b, err := json.Marshal(t.upid)
	reply(w, b, err)
}

// seed decodes the data of path's fixture in dir, else in its sibling
// baseline, into v.
func seed(dir, path string, v any) error {
	b, err := fixture.Read(dir, path)
	if err != nil {
		return fmt.Errorf("proxmoxfake: %w", err)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("proxmoxfake: %s: %w", path, err)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		return fmt.Errorf("proxmoxfake: %s: %w", path, err)
	}
	return nil
}

// Status returns a Workload's status ("running", "stopped"), or "" when
// there is none with that VMID: its true status, even while
// /cluster/resources still lists the old one.
func (p *Proxmox) Status(vmid int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g := p.guest(vmid); g != nil {
		s, _ := g["status"].(string)
		return s
	}
	return ""
}

// Log returns every request in order as "METHOD /path", a power request
// followed by its parameters sorted by name ("forceStop=1 timeout=45").
func (p *Proxmox) Log() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.log)
}

// SetTaskPolls sets how many status reads the tasks started from now on
// take to end.
func (p *Proxmox) SetTaskPolls(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.polls = n
}

// SetStatusLag sets Options.StatusLag for the tasks that end from now on.
func (p *Proxmox) SetStatusLag(reads int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lag = reads
}

// FailNextTask makes the next task end with exitstatus instead of OK,
// leaving its Workload as it was.
func (p *Proxmox) FailNextTask(exitstatus string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNext = exitstatus
}

// FinishTasks ends every running task now, as if each had taken its last
// status read.
func (p *Proxmox) FinishTasks() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range p.tasks {
		p.finish(t)
	}
}

func (p *Proxmox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	entry := r.Method + " " + path
	var params map[string]any
	if (r.Method == http.MethodPost || r.Method == http.MethodDelete) && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&params)
		entry += paramText(params)
	}
	p.mu.Lock()
	p.log = append(p.log, entry)
	p.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && path == resourcesPath:
		p.mu.Lock()
		b, err := json.Marshal(p.resources())
		p.mu.Unlock()
		reply(w, b, err)
	case r.Method == http.MethodPost && strings.Contains(path, "/status/"):
		p.power(w, path)
	case strings.Contains(path, "/snapshot"):
		p.snapshot(w, r.Method, path, params)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/feature"):
		p.feature(w, path)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/vzdump"):
		p.vzdump(w, path, params)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/status") && strings.Contains(path, "/tasks/"):
		p.taskStatus(w, path)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/tasks"):
		p.activeTasks(w, r)
	default:
		p.fixtures.ServeHTTP(w, r)
	}
}

// paramText is a power request's parameters as " k=v k=v", sorted.
func paramText(params map[string]any) string {
	var parts []string
	for k, v := range params {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	slices.Sort(parts)
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

// power answers POST /nodes/{node}/{lxc|qemu}/{vmid}/status/{start|shutdown|reboot}
// with a task's UPID.
func (p *Proxmox) power(w http.ResponseWriter, path string) {
	parts := strings.Split(strings.TrimPrefix(path, api+"/"), "/")
	// nodes {node} {lxc|qemu} {vmid} status {action}
	if len(parts) != 6 || parts[0] != "nodes" || parts[4] != "status" {
		http.Error(w, "no such method", http.StatusNotImplemented)
		return
	}
	node, kind, action := parts[1], parts[2], parts[5]
	vmid, err := strconv.Atoi(parts[3])
	if err != nil {
		http.Error(w, "bad vmid", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.privileged(vmid, "VM.PowerMgmt") {
		// Proxmox: "Permission check failed (/vms/101, VM.PowerMgmt)".
		http.Error(w, fmt.Sprintf("Permission check failed (/vms/%d, VM.PowerMgmt)", vmid), http.StatusForbidden)
		return
	}
	g := p.guest(vmid)
	if g == nil || g["node"] != node || g["type"] != kind {
		http.Error(w, fmt.Sprintf("Configuration file 'nodes/%s/%s/%d.conf' does not exist", node, kind, vmid), http.StatusInternalServerError)
		return
	}
	running := g["status"] == "running"
	prefix := "vz"
	if kind == "qemu" {
		prefix = "qm"
	}
	var effect func(map[string]any)
	switch action {
	case "start":
		if running {
			http.Error(w, fmt.Sprintf("%d already running", vmid), http.StatusInternalServerError)
			return
		}
		effect = func(g map[string]any) { g["status"], g["uptime"] = "running", 1 }
	case "shutdown", "stop":
		if !running {
			http.Error(w, fmt.Sprintf("%d not running", vmid), http.StatusInternalServerError)
			return
		}
		effect = func(g map[string]any) {
			g["status"], g["uptime"], g["cpu"], g["mem"], g["netin"], g["netout"] = "stopped", 0, 0, 0, 0, 0
		}
	case "reboot":
		if !running {
			http.Error(w, fmt.Sprintf("%d not running", vmid), http.StatusInternalServerError)
			return
		}
		effect = func(g map[string]any) { g["uptime"] = 1 }
	default:
		http.Error(w, "no such method", http.StatusNotImplemented)
		return
	}
	t := p.newTask(node, prefix+action, vmid, effect)
	b, err := json.Marshal(t.upid)
	reply(w, b, err)
}

// newTask starts a task of type typ on vmid that applies effect when it
// ends OK (or fails as FailNextTask said). Call with p.mu held.
func (p *Proxmox) newTask(node, typ string, vmid int, effect func(g map[string]any)) *task {
	p.seq++
	started := p.startedAt.Add(time.Duration(p.seq) * time.Second)
	t := &task{
		node: node, typ: typ, id: strconv.Itoa(vmid), vmid: vmid,
		started: started, polls: p.polls, exit: "OK", effect: effect,
	}
	if p.failNext != "" {
		t.exit, p.failNext = p.failNext, ""
	}
	t.upid = fmt.Sprintf("UPID:%s:%08X:%08X:%08X:%s:%s:ddash@pve!dashboard:", node, 0x1000+p.seq, 0x2000+p.seq, started.Unix(), t.typ, t.id)
	p.tasks[t.upid] = t
	return t
}

// taskStatus answers GET /nodes/{node}/tasks/{upid}/status; the task ends
// on its last read.
func (p *Proxmox) taskStatus(w http.ResponseWriter, path string) {
	upid := strings.TrimSuffix(path[strings.Index(path, "/tasks/")+len("/tasks/"):], "/status")
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.tasks[upid]
	if !ok {
		http.Error(w, "no such task", http.StatusInternalServerError)
		return
	}
	t.reads++
	if !t.done && t.reads >= t.polls {
		p.finish(t)
	}
	out := t.json()
	if t.done {
		out["status"], out["exitstatus"] = "stopped", t.exit
	} else {
		out["status"] = "running"
	}
	b, err := json.Marshal(out)
	reply(w, b, err)
}

// activeTasks answers GET /nodes/{node}/tasks?vmid=&source=active with
// the running tasks on that node (and Workload); like Proxmox, without
// source=active it lists none of them.
func (p *Proxmox) activeTasks(w http.ResponseWriter, r *http.Request) {
	node := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, api+"/nodes/"), "/tasks")
	q := r.URL.Query()
	p.mu.Lock()
	defer p.mu.Unlock()
	list := []map[string]any{}
	switch q.Get("source") {
	case "active":
		for _, t := range p.tasks {
			if t.done || t.node != node || (q.Get("vmid") != "" && q.Get("vmid") != t.id) {
				continue
			}
			list = append(list, t.json())
		}
	case "all":
		// Running and ended; a listing counts as a status read of each
		// running task, so a task followed this way ends too.
		for _, t := range p.tasks {
			if t.node != node || (q.Get("vmid") != "" && q.Get("vmid") != t.id) {
				continue
			}
			if !t.done {
				if t.reads++; t.reads >= t.polls {
					p.finish(t)
				}
			}
			e := t.json()
			if t.done {
				e["status"], e["endtime"] = t.exit, t.started.Unix()+int64(t.reads)
			}
			list = append(list, e)
		}
	}
	slices.SortFunc(list, func(a, b map[string]any) int { return strings.Compare(a["upid"].(string), b["upid"].(string)) })
	b, err := json.Marshal(list)
	reply(w, b, err)
}

func (t *task) json() map[string]any {
	return map[string]any{
		"upid": t.upid, "node": t.node, "type": t.typ, "id": t.id,
		"user": "ddash@pve!dashboard", "starttime": t.started.Unix(),
	}
}

// finish ends t, applying its change when it ended OK. Call with p.mu held.
func (p *Proxmox) finish(t *task) {
	if t.done {
		return
	}
	t.done = true
	if t.exit != "OK" {
		return
	}
	if g := p.guest(t.vmid); g != nil {
		if p.lag > 0 {
			p.stale[t.vmid] = &staleGuest{guest: maps.Clone(g), reads: p.lag}
		}
		t.effect(g)
	}
}

// resources is what /cluster/resources lists now: each Workload, or as it
// was while its change is still stale (counting this read). Call with
// p.mu held.
func (p *Proxmox) resources() []map[string]any {
	out := make([]map[string]any, len(p.guests))
	for i, g := range p.guests {
		out[i] = g
		id, _ := g["vmid"].(float64)
		if s, ok := p.stale[int(id)]; ok {
			out[i] = s.guest
			if s.reads--; s.reads <= 0 {
				delete(p.stale, int(id))
			}
		}
	}
	return out
}

// guest returns the Workload with vmid. Call with p.mu held.
func (p *Proxmox) guest(vmid int) map[string]any {
	for _, g := range p.guests {
		if id, ok := g["vmid"].(float64); ok && int(id) == vmid {
			return g
		}
	}
	return nil
}

// privileged reports whether the token holds priv on /vms/<vmid>, from the
// nearest of /vms/<vmid>, /vms and /. Call with p.mu held.
func (p *Proxmox) privileged(vmid int, priv string) bool {
	for i, path := range []string{fmt.Sprintf("/vms/%d", vmid), "/vms", "/"} {
		if privs, ok := p.perms[path]; ok {
			// From a parent path only a privilege that propagates holds.
			propagate, held := privs[priv]
			return held && (i == 0 || propagate == 1)
		}
	}
	return false
}

// reply wraps data in Proxmox's {"data": ...} envelope.
func reply(w http.ResponseWriter, data []byte, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"data":%s}`, data)
}
