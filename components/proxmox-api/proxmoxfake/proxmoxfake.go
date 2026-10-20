// Package proxmoxfake is a stateful fake of the Proxmox VE API for
// proxmox-api's power Actions. It keeps each Workload's status, refuses a
// power request where the token lacks VM.PowerMgmt (as Proxmox's ACL
// does), runs each power request as a task that ends after a number of
// status reads, and records every request in order; anything else is
// served from a scenario's fixtures (fixture.Scenario). proxmox-api's
// tests and fakelab serve it.
package proxmoxfake

import (
	"encoding/json"
	"fmt"
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
}

// Proxmox is the fake, an http.Handler.
type Proxmox struct {
	fixtures http.Handler

	mu        sync.Mutex
	guests    []map[string]any
	perms     map[string]map[string]int
	polls     int
	failNext  string
	tasks     map[string]*task
	seq       int
	log       []string
	startedAt time.Time
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
	return p, nil
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
// there is none with that VMID.
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
	if r.Method == http.MethodPost && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&params)
		entry += paramText(params)
	}
	p.mu.Lock()
	p.log = append(p.log, entry)
	p.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && path == resourcesPath:
		p.mu.Lock()
		b, err := json.Marshal(p.guests)
		p.mu.Unlock()
		reply(w, b, err)
	case r.Method == http.MethodPost && strings.Contains(path, "/status/"):
		p.power(w, path)
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
	p.seq++
	started := p.startedAt.Add(time.Duration(p.seq) * time.Second)
	t := &task{
		node: node, typ: prefix + action, id: strconv.Itoa(vmid), vmid: vmid,
		started: started, polls: p.polls, exit: "OK", effect: effect,
	}
	if p.failNext != "" {
		t.exit, p.failNext = p.failNext, ""
	}
	t.upid = fmt.Sprintf("UPID:%s:%08X:%08X:%08X:%s:%s:ddash@pve!dashboard:", node, 0x1000+p.seq, 0x2000+p.seq, started.Unix(), t.typ, t.id)
	p.tasks[t.upid] = t
	b, err := json.Marshal(t.upid)
	reply(w, b, err)
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
	if q.Get("source") == "active" {
		for _, t := range p.tasks {
			if t.done || t.node != node || (q.Get("vmid") != "" && q.Get("vmid") != t.id) {
				continue
			}
			list = append(list, t.json())
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
		t.effect(g)
	}
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
