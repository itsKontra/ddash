package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Jobs runs an Agent's long operations, at most one per target at a time,
// and keeps them in memory until ddash collects them (or KeepFor after
// they ended). A job outlives the call that started it; it is lost only
// when the Agent itself stops.
type Jobs struct {
	now func() time.Time

	mu   sync.Mutex
	seq  int
	jobs map[string]*Job
	// life ends every job's context when the Agent stops.
	life context.Context
}

// KeepFor is how long an ended job nobody collected is kept.
const KeepFor = 24 * time.Hour

// logLines is how many of its last output lines a step keeps.
const logLines = 20

// NewJobs returns an empty registry.
func NewJobs(now func() time.Time) *Jobs {
	if now == nil {
		now = time.Now
	}
	return &Jobs{now: now, jobs: map[string]*Job{}, life: context.Background()}
}

// SetContext makes every job's context end with ctx.
func (j *Jobs) SetContext(ctx context.Context) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.life = ctx
}

// ErrBusy is Start's refusal while a job runs on the same target.
var ErrBusy = errors.New("busy")

var errNoJob = errors.New("no such job")

// Run is a job's body. It reports through p and returns the job's result,
// or why it failed.
type Run func(ctx context.Context, p *Progress) (map[string]string, error)

// Start registers a job with steps (their titles) and runs it in the
// background. It refuses with ErrBusy while another job on target runs.
func (j *Jobs) Start(op, title, target string, steps []string, run Run) (Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, other := range j.jobs {
		if target != "" && other.Target == target && !other.State.Ended() {
			return Job{}, fmt.Errorf("%w: %s is already running on %s", ErrBusy, other.Title, target)
		}
	}
	j.sweep()
	j.seq++
	job := &Job{
		ID:     strconv.FormatInt(j.now().UnixMilli(), 36) + "-" + strconv.Itoa(j.seq),
		Op:     op,
		Title:  title,
		Target: target,
		State:  Running,
		Start:  j.now().UTC(),
	}
	for _, s := range steps {
		job.Steps = append(job.Steps, Step{Title: s, State: Pending})
	}
	j.jobs[job.ID] = job
	p := &Progress{jobs: j, id: job.ID}
	ctx := j.life
	go func() {
		var result map[string]string
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("the Agent's job panicked: %v", r)
				}
			}()
			result, err = run(ctx, p)
		}()
		p.finish(result, err)
	}()
	return clone(job), nil
}

// sweep forgets ended jobs older than KeepFor. Call with j.mu held.
func (j *Jobs) sweep() {
	for id, job := range j.jobs {
		if job.State.Ended() && j.now().Sub(job.End) > KeepFor {
			delete(j.jobs, id)
		}
	}
}

// List returns every job, oldest first.
func (j *Jobs) List() []Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.sweep()
	out := []Job{}
	for _, job := range j.jobs {
		out = append(out, clone(job))
	}
	slices.SortFunc(out, func(a, b Job) int { return a.Start.Compare(b.Start) })
	return out
}

// Get returns one job.
func (j *Jobs) Get(id string) (Job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.jobs[id]
	if !ok {
		return Job{}, false
	}
	return clone(job), true
}

// Collect forgets an ended job and returns it; a running one is refused.
func (j *Jobs) Collect(id string) (Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.jobs[id]
	switch {
	case !ok:
		return Job{}, fmt.Errorf("%w %s", errNoJob, id)
	case !job.State.Ended():
		return Job{}, fmt.Errorf("job %s is still running", id)
	}
	delete(j.jobs, id)
	return clone(job), nil
}

func clone(j *Job) Job {
	c := *j
	c.Steps = slices.Clone(j.Steps)
	for i := range c.Steps {
		c.Steps[i].Log = slices.Clone(j.Steps[i].Log)
	}
	if j.Result != nil {
		c.Result = map[string]string{}
		for k, v := range j.Result {
			c.Result[k] = v
		}
	}
	return c
}

// Progress is how a running job reports its steps.
type Progress struct {
	jobs *Jobs
	id   string
}

func (p *Progress) edit(f func(*Job)) {
	p.jobs.mu.Lock()
	defer p.jobs.mu.Unlock()
	if job, ok := p.jobs.jobs[p.id]; ok {
		f(job)
	}
}

// Begin marks step i running.
func (p *Progress) Begin(i int) {
	p.edit(func(j *Job) { j.Steps[i].State = Running })
}

// Log adds output lines to step i, keeping the last few.
func (p *Progress) Log(i int, text string) {
	p.edit(func(j *Job) {
		for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				j.Steps[i].Log = append(j.Steps[i].Log, line)
			}
		}
		if n := len(j.Steps[i].Log); n > logLines {
			j.Steps[i].Log = j.Steps[i].Log[n-logLines:]
		}
	})
}

// Done marks step i done, with an optional detail ("3 packages").
func (p *Progress) Done(i int, detail string) {
	p.edit(func(j *Job) { j.Steps[i].State, j.Steps[i].Detail = Done, detail })
}

// Skip marks step i skipped, saying why.
func (p *Progress) Skip(i int, why string) {
	p.edit(func(j *Job) { j.Steps[i].State, j.Steps[i].Detail = Skipped, why })
}

// finish ends the job: a step still running fails with err, and steps
// never begun stay pending.
func (p *Progress) finish(result map[string]string, err error) {
	p.edit(func(j *Job) {
		j.End = p.jobs.now().UTC()
		j.Result = result
		if err == nil {
			j.State = Done
			return
		}
		j.State, j.Error = Failed, err.Error()
		for i := range j.Steps {
			if j.Steps[i].State == Running {
				j.Steps[i].State = Failed
				if j.Steps[i].Detail == "" {
					j.Steps[i].Detail = err.Error()
				}
			}
		}
	})
}
