package power

import (
	"sync"

	"gitlab.com/akita/util/tracing"
)

// PowerStatTracer can collect the total time of a certain step is triggerred.
type PowerStatTracer struct {
	filter    tracing.TaskFilter
	lock      sync.Mutex
	stepNames []string
	stepCount map[string]uint64
}

// NewPowerStatTracer creates a new PowerStatTracer
func NewPowerStatTracer(filter tracing.TaskFilter) *PowerStatTracer {
	t := &PowerStatTracer{
		filter:    filter,
		stepCount: make(map[string]uint64),
	}
	return t
}

func (t *PowerStatTracer) Reset() {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.stepNames = []string{}
	t.stepCount = make(map[string]uint64)
}

// GetStepNames returns all the step names collected.
func (t *PowerStatTracer) GetStepNames() []string {
	return t.stepNames
}

// GetStepCount returns the number of steps that is recorded with a certain step
// name.
func (t *PowerStatTracer) GetStepCount(stepName string) uint64 {
	return t.stepCount[stepName]
}

// StartTask records the task start time
func (t *PowerStatTracer) StartTask(task tracing.Task) {}

// StepTask does nothing
func (t *PowerStatTracer) StepTask(task tracing.Task) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if !t.filter(task) {
		return
	}

	if task.Detail != nil {
		t.countMultipleStep(task, task.Detail.(uint64))
	} else {
		t.countStep(task)
	}
}

func (t *PowerStatTracer) countStep(task tracing.Task) {
	step := task.Steps[0]
	_, ok := t.stepCount[step.What]
	if !ok {
		t.stepNames = append(t.stepNames, step.What)
	}
	t.stepCount[step.What]++
}

func (t *PowerStatTracer) countMultipleStep(task tracing.Task, count uint64) {
	step := task.Steps[0]
	_, ok := t.stepCount[step.What]
	if !ok {
		t.stepNames = append(t.stepNames, step.What)
	}
	t.stepCount[step.What] += count
}

func taskContainsStep(task tracing.Task, step tracing.TaskStep) bool {
	for _, s := range task.Steps {
		if s.What == step.What {
			return true
		}
	}

	return false
}

// EndTask records the end of the task
func (t *PowerStatTracer) EndTask(task tracing.Task) {}
