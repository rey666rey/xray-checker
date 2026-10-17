package checker

import (
	"context"
	"sync"
	"sync/atomic"
)

// All background probes share the configured connection budget. Diagnostics
// additionally have a small allowance, leaving capacity for ordinary checks and
// replacement verification even when every diagnosed endpoint times out.
type probeBudget struct {
	slots       chan struct{}
	diagnostics chan struct{}
	active      atomic.Int64
}

func newProbeBudget(limit int) *probeBudget {
	budget := &probeBudget{}
	diagnosticLimit := 1
	if limit > 0 {
		budget.slots = make(chan struct{}, limit)
		diagnosticLimit = limit / 10
		if diagnosticLimit < 1 {
			diagnosticLimit = 1
		}
		if diagnosticLimit > 4 {
			diagnosticLimit = 4
		}
	}
	budget.diagnostics = make(chan struct{}, diagnosticLimit)
	return budget
}

func (b *probeBudget) acquire(ctx context.Context, diagnostic bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if diagnostic {
		select {
		case b.diagnostics <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if b.slots != nil {
		select {
		case b.slots <- struct{}{}:
		case <-ctx.Done():
			if diagnostic {
				<-b.diagnostics
			}
			return nil, ctx.Err()
		}
	}
	b.active.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			b.active.Add(-1)
			if b.slots != nil {
				<-b.slots
			}
			if diagnostic {
				<-b.diagnostics
			}
		})
	}, nil
}

func (pc *ProxyChecker) cancelAutomaticDiagnosis() {
	pc.diagnosisMu.RLock()
	cancel := pc.diagnosisCancel
	automatic := pc.diagnosisAutomatic
	pc.diagnosisMu.RUnlock()
	if automatic && cancel != nil {
		cancel()
	}
}
