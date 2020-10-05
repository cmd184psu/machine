//go:generate godocdown -o README.md

package machine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// if a goroutine returns this error, every goroutines context will be cancelled
var Cancel = errors.New("[machine] cancel")

/*
Machine is a runtime for managed goroutines. It is inspired by errgroup.Group with extra bells & whistles:

- throttled goroutines

- self-cancellable goroutines with context

- global-cancellable goroutines with context (see Cancel)

- tagging goroutines for debugging(see Stats)

- publish/subscribe to channels for passing messages between goroutines

*/
type Machine struct {
	subChanLength int
	pubChanLength int
	cancel        func()
	ctx           context.Context
	errs          []error
	mu            sync.RWMutex
	routines      map[string]Routine
	max           int
	closeOnce     sync.Once
	debug         bool
	subscriptions map[string]map[string]chan interface{}
	subMu         sync.RWMutex
}

// New Creates a new machine instance with the given root context & options
func New(ctx context.Context, options ...Opt) (*Machine, error) {
	opts := &option{}
	for _, o := range options {
		o(opts)
	}
	if opts.maxRoutines <= 0 {
		opts.maxRoutines = 10000
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Machine{
		subChanLength: opts.subChannelLength,
		pubChanLength: opts.pubChannelLength,
		cancel:        cancel,
		ctx:           ctx,
		errs:          nil,
		mu:            sync.RWMutex{},
		routines:      map[string]Routine{},
		max:           opts.maxRoutines,
		closeOnce:     sync.Once{},
		debug:         false,
		subscriptions: map[string]map[string]chan interface{}{},
		subMu:         sync.RWMutex{},
	}, nil
}

// Current returns current managed goroutine count
func (p *Machine) Current() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.routines)
}

func (m *Machine) addRoutine(tags ...string) Routine {
	child, cancel := context.WithCancel(m.ctx)
	var x int
	for x = m.Current(); x >= m.max; x = m.Current() {
		if m.ctx.Err() != nil {
			cancel()
			return nil
		}
	}
	id := uuid()
	routine := &goRoutine{
		machine:  m,
		addedAt:  x,
		ctx:      child,
		id:       id,
		tags:     tags,
		start:    time.Now(),
		doneOnce: sync.Once{},
		cancel:   cancel,
	}
	m.mu.Lock()
	m.routines[id] = routine
	m.mu.Unlock()
	return routine
}

// Go calls the given function in a new goroutine.
//
// The first call to return a non-nil error who's cause is CancelGroup cancels the context of every job.
// All errors that are not CancelGroup will be returned by Wait.
func (m *Machine) Go(fn func(routine Routine) error, tags ...string) {
	routine := m.addRoutine(tags...)
	go func() {
		defer routine.Done()
		if err := fn(routine); err != nil {
			if errors.Unwrap(err) == Cancel {
				m.Cancel()
			} else {
				m.addErr(err)
			}
		}
	}()
}

func (p *Machine) addErr(err error) {
	p.errs = append(p.errs, err)
}

// Wait waites for all goroutines to exit
func (p *Machine) Wait() []error {
	for p.Current() != 0 {
	}
	p.Cancel()
	return p.errs
}

// Cancel cancels every goroutines context
func (p *Machine) Cancel() {
	p.closeOnce.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
	})
}

// Stats returns Goroutine information from the machine
func (m *Machine) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.subMu.RLock()
	defer m.subMu.RUnlock()
	copied := map[string]RoutineStats{}
	for k, v := range m.routines {
		if v != nil {
			copied[k] = RoutineStats{
				ID:            v.ID(),
				Start:         v.Start(),
				Duration:      v.Duration(),
				Tags:          v.Tags(),
				Subscriptions: v.Subscriptions(),
			}
		}
	}
	return Stats{
		Count:    len(copied),
		Routines: copied,
	}
}

func (p *Machine) debugf(format string, a ...interface{}) {
	if p.debug {
		fmt.Printf(format, a...)
	}
}
