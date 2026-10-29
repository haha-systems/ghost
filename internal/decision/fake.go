package decision

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

type FakeStep struct {
	Response core.DecisionResponse
	Err      error
	Delay    time.Duration
	Started  chan<- struct{}
	Release  <-chan struct{}
}

// FakeAgent returns scripted results and keeps a copy of each request.
type FakeAgent struct {
	mu       sync.Mutex
	info     Info
	steps    []FakeStep
	requests []core.DecisionRequest
	next     int
}

func NewFakeAgent(info Info, steps ...FakeStep) *FakeAgent {
	return &FakeAgent{info: cloneInfo(info), steps: append([]FakeStep(nil), steps...)}
}

func (a *FakeAgent) Info() Info {
	a.mu.Lock()
	defer a.mu.Unlock()
	return cloneInfo(a.info)
}

func (a *FakeAgent) Requests() []core.DecisionRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	requests := make([]core.DecisionRequest, len(a.requests))
	for i, request := range a.requests {
		requests[i] = cloneRequest(request)
	}
	return requests
}

func (a *FakeAgent) Decide(ctx context.Context, request core.DecisionRequest) (core.DecisionResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	a.requests = append(a.requests, cloneRequest(request))
	if a.next >= len(a.steps) {
		a.mu.Unlock()
		return core.DecisionResponse{}, errors.New("fake decision has no scripted result")
	}
	step := a.steps[a.next]
	a.next++
	a.mu.Unlock()
	if step.Started != nil {
		select {
		case step.Started <- struct{}{}:
		case <-ctx.Done():
			return core.DecisionResponse{}, ctx.Err()
		}
	}
	if step.Release != nil {
		select {
		case <-step.Release:
		case <-ctx.Done():
			return core.DecisionResponse{}, ctx.Err()
		}
	}
	if step.Delay > 0 {
		timer := time.NewTimer(step.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return core.DecisionResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
	if step.Err != nil {
		return core.DecisionResponse{}, step.Err
	}
	response := step.Response
	if response.RequestID == "" {
		response.RequestID = request.ID
	}
	response.ModelOutput = append([]byte(nil), response.ModelOutput...)
	if response.Supporting != nil {
		supporting := make(map[string]string, len(response.Supporting))
		for key, value := range response.Supporting {
			supporting[key] = value
		}
		response.Supporting = supporting
	}
	return response, nil
}
