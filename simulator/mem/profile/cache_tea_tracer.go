package profile

import (
	"sync"

	"gitlab.com/akita/util/tracing"
)

type CacheUtilizationTracer struct {
	filter    tracing.TaskFilter
	lock      sync.Mutex
	stepCount map[string]float64
}

func NewCacheUtilizationTracer(filter tracing.TaskFilter) *CacheUtilizationTracer {
	t := &CacheUtilizationTracer{
		filter:    filter,
		stepCount: make(map[string]float64),
	}
	return t
}

// TotalCount returns the total number of tasks.
func (t *CacheUtilizationTracer) GetStepCount(name string) float64 {
	t.lock.Lock()
	defer t.lock.Unlock()

	if count, ok := t.stepCount[name]; ok {
		return count
	}

	return 0
}

// StartTask records the task start time
func (t *CacheUtilizationTracer) StartTask(task tracing.Task) {
	if !t.filter(task) {
		return
	}
	t.lock.Lock()
	value, ok := task.Detail.(float64)
	if !ok {
		panic("task detail is not float64")
	}
	t.stepCount[task.What] += value

	t.lock.Unlock()
}

// StepTask does nothing
func (t *CacheUtilizationTracer) StepTask(task tracing.Task) {
	// Do nothing
}

// EndTask records the end of the task
func (t *CacheUtilizationTracer) EndTask(task tracing.Task) {
}
