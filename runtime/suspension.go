package runtime

import (
	"fmt"
	"sync"
)

type YieldRequest struct {
	Payload any
}

func Yield(payload any) error {
	return &YieldRequest{Payload: payload}
}

func (y *YieldRequest) Error() string {
	if y == nil {
		return "runtime: host yielded"
	}
	return fmt.Sprintf("runtime: host yielded: %v", y.Payload)
}

type Suspension struct {
	Payload any

	mu       sync.Mutex
	resumed  bool
	resume   func([]Value) ([]Value, error)
	snapshot func() (*Snapshot, error)
}

func NewSuspension(payload any, resume func([]Value) ([]Value, error)) *Suspension {
	return NewSuspensionWithSnapshot(payload, resume, nil)
}

func NewSuspensionWithSnapshot(payload any, resume func([]Value) ([]Value, error), snapshot func() (*Snapshot, error)) *Suspension {
	return &Suspension{
		Payload:  payload,
		resume:   resume,
		snapshot: snapshot,
	}
}

func (s *Suspension) Error() string {
	if s == nil {
		return "runtime: vm suspended"
	}
	return fmt.Sprintf("runtime: vm suspended: %v", s.Payload)
}

func (s *Suspension) ResumeValues(results ...Value) ([]Value, error) {
	if s == nil {
		return nil, fmt.Errorf("runtime: missing suspension")
	}
	s.mu.Lock()
	if s.resumed {
		s.mu.Unlock()
		return nil, fmt.Errorf("runtime: suspension already resumed")
	}
	s.resumed = true
	resume := s.resume
	s.mu.Unlock()

	if resume == nil {
		return nil, fmt.Errorf("runtime: suspension cannot be resumed")
	}
	return resume(results)
}

func (s *Suspension) Snapshot() (*Snapshot, error) {
	if s == nil {
		return nil, fmt.Errorf("runtime: missing suspension")
	}
	s.mu.Lock()
	snapshot := s.snapshot
	s.mu.Unlock()
	if snapshot == nil {
		return nil, fmt.Errorf("runtime: suspension cannot create snapshot")
	}
	return snapshot()
}
