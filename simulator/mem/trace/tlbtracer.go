// Package trace provides a tlbTracer that can trace memory system tasks.
package trace

import (
	"log"

	"gitlab.com/akita/util/tracing"
)

// A tlbTracer is a hook that can record the actions of a memory model into
// traces.
type tlbTracer struct {
	logger *log.Logger
}

// StartTask marks the start of a memory transaction
func (t *tlbTracer) StartTask(task tracing.Task) {
}

// StepTask marks the memory transaction has completed a milestone
func (t *tlbTracer) StepTask(task tracing.Task) {
	if !(task.Steps[0].What == "tlb-miss" ||
		task.Steps[0].What == "tlb-hit" ||
		task.Steps[0].What == "tlb-mshr-hit") {
		return
	}

	t.logger.Printf("%s, %s, %X\n",
		task.ID,
		task.Steps[0].What,
		task.Detail,
	)
}

// EndTask marks the end of a memory transaction
func (t *tlbTracer) EndTask(task tracing.Task) {
}

// NewTracer creates a new Tracer.
func NewTLBTracer(logger *log.Logger) tracing.Tracer {
	t := new(tlbTracer)
	t.logger = logger
	return t
}
