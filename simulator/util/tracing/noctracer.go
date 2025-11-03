package tracing

import (
	"sync"

	"gitlab.com/akita/akita"
)

// NocTracer can collect the total time of executing a certain type of
// task. If the execution of two tasks overlaps, this tracer will simply add
// the two task processing time together.
type NocTracer struct {
	filter        TaskFilter
	lock          sync.Mutex
	averageTime   akita.VTimeInSec
	inflightTasks map[string]Task
	taskCount     uint64
}

// NewTranslationReqTracer creates a new TranslationReqTracer
func NewNocTracer(filter TaskFilter) *NocTracer {
	t := &NocTracer{
		filter:        filter,
		inflightTasks: make(map[string]Task),
	}
	return t
}

// AverageTime returns the total time has been spent on a certain type of tasks.
func (t *NocTracer) AverageTime() akita.VTimeInSec {
	t.lock.Lock()
	time := t.averageTime
	t.lock.Unlock()
	return time
}

// TotalCount returns the total number of tasks.
func (t *NocTracer) TotalCount() uint64 {
	t.lock.Lock()
	defer t.lock.Unlock()

	return t.taskCount
}

// StartTask records the task start time
func (t *NocTracer) StartTask(task Task) {
	if !t.filter(task) {
		return
	}
	t.lock.Lock()
	if _, exists := t.inflightTasks[task.ID]; !exists {
		t.inflightTasks[task.ID] = task
	}
	t.lock.Unlock()
}

// StepTask does nothing
func (t *NocTracer) StepTask(task Task) {
}

// EndTask records the end of the task
func (t *NocTracer) EndTask(task Task) {
	t.lock.Lock()
	originalTask, ok := t.inflightTasks[task.ID]
	if !ok {
		t.lock.Unlock()
		return
	}

	taskTime := task.EndTime - originalTask.StartTime
	t.averageTime = akita.VTimeInSec(
		(float64(t.averageTime)*float64(t.taskCount) + float64(taskTime)) /
			float64(t.taskCount+1))
	delete(t.inflightTasks, task.ID)
	t.taskCount++
	t.lock.Unlock()
}
