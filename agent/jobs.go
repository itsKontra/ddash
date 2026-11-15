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
	// cancels ends the context of each running job ddash may cancel.
	cancels map[string]context.CancelCauseFunc
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
	return &Jobs{now: now, jobs: map[string]*Job{}, cancels: map[string]context.CancelCauseFunc{}, life: context.Background()}
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

// Cancel's refusals: the job already ended, or it isn't one ddash may
// cancel.
var (
	ErrEnded          = errors.New("ended")
	ErrNotCancellable = errors.New("not cancellable")
)

// errCancelled ends a cancelled job's context.
var errCancelled = errors.New("the job was cancelled")

// Run is a job's body. It reports through p and returns the job's result,
// or why it failed.
type Run func(ctx context.Context, p *Progress) (map[string]string, error)

// Start registers a job with steps (their titles) and runs it in the
// background. It refuses with ErrBusy while another job on target runs.
// The job can't be cancelled: Start is for an operation that a command
// stopped part-way may leave the host broken by, such as a package
// upgrade or the Agent's own update.
func (j *Jobs) Start(op, title, target string, steps []string, run Run) (Job, error) {
	return j.start(op, title, target, steps, run, false)
}

// StartCancellable is Start for a job ddash may Cancel: one whose command,
// stopped part-way, leaves nothing that running it again doesn't mend,
// such as a Compose operation.
func (j *Jobs) StartCancellable(op, title, target string, steps []string, run Run) (Job, error) {
	return j.start(op, title, target, steps, run, true)
}

func (j *Jobs) start(op, title, target string, steps []string, run Run, cancellable bool) (Job, error) {
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
	life, cancelLife := context.WithCancelCause(j.life)
	if cancellable {
		j.cancels[job.ID] = cancelLife
	}
	ctx, cancel := context.WithTimeout(life, JobTimeout)
	type outcome struct {
		result map[string]string
		err    error
	}
	ended := make(chan outcome, 1)
	go func() {
		var o outcome
		func() {
			defer func() {
				if r := recover(); r != nil {
					o.err = fmt.Errorf("the Agent's job panicked: %v", r)
				}
			}()
			o.result, o.err = run(ctx, p)
		}()
		ended <- o
	}()
	go func() {
		defer cancel()
		defer cancelLife(nil)
		// stopped is why ctx ended when a cancel or the timeout stopped
		// the job, else nil.
		stopped := func() error {
			switch cause := context.Cause(ctx); {
			case errors.Is(cause, errCancelled):
				return cause
			case errors.Is(cause, context.DeadlineExceeded):
				return fmt.Errorf("the job didn't end within %s and was stopped", JobTimeout)
			}
			return nil
		}
		select {
		case o := <-ended:
			if why := stopped(); o.err != nil && why != nil {
				o.err = why
			}
			p.finish(o.result, o.err)
		case <-ctx.Done():
			why := stopped()
			if why == nil {
				// The Agent is stopping: the job ends with it.
				o := <-ended
				p.finish(o.result, o.err)
				return
			}
			// Its commands were killed with ctx; a body that still
			// doesn't return is given up on.
			select {
			case o := <-ended:
				if o.err == nil {
					p.finish(o.result, nil)
					return
				}
			case <-time.After(stopGrace):
			}
			p.finish(nil, why)
		}
	}()
	return clone(job), nil
}

// Cancel stops the running job id: its context ends, which kills the
// command it runs, and it ends Cancelled (or Done, when it got to its end
// first). A body that doesn't return is given up on after the same grace
// as at the timeout. It refuses ErrEnded for a job that ended, and
// ErrNotCancellable for one started with Start. Cancelling a job that is
// still stopping again is no error.
func (j *Jobs) Cancel(id string) (Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.jobs[id]
	switch {
	case !ok:
		return Job{}, fmt.Errorf("%w %s", errNoJob, id)
	case job.State.Ended():
		return Job{}, fmt.Errorf("%w: %s already ended (%s)", ErrEnded, job.Title, job.State)
	}
	stop, ok := j.cancels[id]
	if !ok {
		return Job{}, fmt.Errorf("%w: %s can't be cancelled, since a command stopped part-way could leave the host broken; it ends by itself", ErrNotCancellable, job.Title)
	}
	stop(errCancelled)
	return clone(job), nil
}

// JobTimeout is how long a job may run: far longer than a pull of large
// images or a distribution's upgrade takes, so only a job that hangs
// reaches it. Its ctx ends then, which kills the command it runs.
var JobTimeout = 2 * time.Hour

// stopGrace is how long a job has to return once its ctx ended.
const stopGrace = 10 * time.Second

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

// edit changes the job while it runs. A body that reports after its job
// ended (it was given up on) changes nothing.
func (p *Progress) edit(f func(*Job)) {
	p.jobs.mu.Lock()
	defer p.jobs.mu.Unlock()
	if job, ok := p.jobs.jobs[p.id]; ok && !job.State.Ended() {
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
// never begun stay pending. A cancelled job (err is errCancelled) and its
// running step end Cancelled, with no error.
func (p *Progress) finish(result map[string]string, err error) {
	p.edit(func(j *Job) {
		delete(p.jobs.cancels, p.id)
		j.End = p.jobs.now().UTC()
		j.Result = result
		if err == nil {
			j.State = Done
			return
		}
		state, detail := Failed, err.Error()
		if errors.Is(err, errCancelled) {
			state, detail = Cancelled, "cancelled"
		} else {
			j.Error = err.Error()
		}
		j.State = state
		for i := range j.Steps {
			if j.Steps[i].State == Running {
				j.Steps[i].State = state
				if j.Steps[i].Detail == "" || state == Cancelled {
					j.Steps[i].Detail = detail
				}
			}
		}
	})
}
