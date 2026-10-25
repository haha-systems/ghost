// Package decision provides bounded, advisory decision agents.
package decision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

const (
	DefaultTimeout          = 5 * time.Second
	DefaultMaxPerRun        = 20
	DefaultMaxPerTask       = 3
	DefaultFailureThreshold = 3
)

type Info struct {
	ID           string              `json:"id"`
	Version      string              `json:"version,omitempty"`
	Kinds        []core.DecisionKind `json:"kinds"`
	Capabilities []core.Capability   `json:"capabilities,omitempty"`
}

func cloneInfo(info Info) Info {
	info.Kinds = append([]core.DecisionKind(nil), info.Kinds...)
	info.Capabilities = append([]core.Capability(nil), info.Capabilities...)
	return info
}

func cloneRequest(request core.DecisionRequest) core.DecisionRequest {
	request.State = append([]byte(nil), request.State...)
	request.Actions = append([]string(nil), request.Actions...)
	request.MemoryEvidence = append([]string(nil), request.MemoryEvidence...)
	return request
}

func cloneResponse(response core.DecisionResponse) core.DecisionResponse {
	response.ModelOutput = append([]byte(nil), response.ModelOutput...)
	if response.Supporting != nil {
		supporting := make(map[string]string, len(response.Supporting))
		for key, value := range response.Supporting {
			supporting[key] = value
		}
		response.Supporting = supporting
	}
	return response
}

func (i Info) validate() error {
	if strings.TrimSpace(i.ID) == "" {
		return errors.New("decision agent ID is required")
	}
	if len(i.Kinds) == 0 {
		return errors.New("decision agent kinds are required")
	}
	seen := map[core.DecisionKind]bool{}
	for _, kind := range i.Kinds {
		if err := kind.Validate(); err != nil {
			return err
		}
		if seen[kind] {
			return fmt.Errorf("duplicate decision kind %q", kind)
		}
		seen[kind] = true
	}
	seenCapabilities := map[core.Capability]bool{}
	for _, capability := range i.Capabilities {
		if err := capability.Validate(); err != nil {
			return err
		}
		if seenCapabilities[capability] {
			return fmt.Errorf("duplicate decision capability %q", capability)
		}
		seenCapabilities[capability] = true
	}
	return nil
}

type Agent interface {
	Info() Info
	Decide(context.Context, core.DecisionRequest) (core.DecisionResponse, error)
}

type Status string

type OutcomeStatus = Status

const (
	StatusRecommended      Status = "recommended"
	StatusUnavailable      Status = "unavailable"
	StatusDisabled         Status = "disabled"
	StatusBudgetExhausted  Status = "budget_exhausted"
	StatusInvalid          Status = "invalid"
	StatusError            Status = "error"
	OutcomeRecommended            = StatusRecommended
	OutcomeUnavailable            = StatusUnavailable
	OutcomeDisabled               = StatusDisabled
	OutcomeBudgetExhausted        = StatusBudgetExhausted
	OutcomeInvalid                = StatusInvalid
	OutcomeError                  = StatusError
)

type Outcome struct {
	Status   Status                 `json:"status"`
	Response *core.DecisionResponse `json:"response,omitempty"`
	Fallback string                 `json:"fallback,omitempty"`
	Reason   string                 `json:"reason,omitempty"`
}

type ServiceConfig struct {
	Enabled          map[core.DecisionKind]bool
	Timeout          time.Duration
	MaxPerRun        int
	MaxPerTask       int
	FailureThreshold int
	Fallback         string
}

type Service struct {
	mu               sync.Mutex
	agents           map[core.DecisionKind]Agent
	infos            map[core.DecisionKind]Info
	enabled          map[core.DecisionKind]bool
	knownEnabled     map[core.DecisionKind]bool
	failures         map[core.DecisionKind]int
	open             map[core.DecisionKind]bool
	runCalls         int
	taskCalls        map[core.TaskID]int
	timeout          time.Duration
	maxPerRun        int
	maxPerTask       int
	failureThreshold int
	fallback         string
}

func NewService(config ServiceConfig) *Service {
	if config.Timeout <= 0 {
		config.Timeout = DefaultTimeout
	}
	if config.MaxPerRun <= 0 {
		config.MaxPerRun = DefaultMaxPerRun
	}
	if config.MaxPerTask <= 0 {
		config.MaxPerTask = DefaultMaxPerTask
	}
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = DefaultFailureThreshold
	}
	known := make(map[core.DecisionKind]bool, len(config.Enabled))
	enabled := make(map[core.DecisionKind]bool, len(config.Enabled))
	for kind, value := range config.Enabled {
		known[kind] = true
		enabled[kind] = value
	}
	return &Service{
		agents: map[core.DecisionKind]Agent{}, infos: map[core.DecisionKind]Info{}, enabled: enabled, knownEnabled: known,
		failures: map[core.DecisionKind]int{}, open: map[core.DecisionKind]bool{}, taskCalls: map[core.TaskID]int{},
		timeout: config.Timeout, maxPerRun: config.MaxPerRun, maxPerTask: config.MaxPerTask,
		failureThreshold: config.FailureThreshold, fallback: config.Fallback,
	}
}

func (s *Service) Register(agent Agent) error {
	if s == nil || agent == nil {
		return errors.New("decision service and agent are required")
	}
	info, err := safeInfo(agent)
	if err != nil {
		return err
	}
	if err := info.validate(); err != nil {
		return fmt.Errorf("decision agent %q: %w", info.ID, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	for _, kind := range info.Kinds {
		if _, exists := s.agents[kind]; exists {
			return fmt.Errorf("decision kind %q already has an agent", kind)
		}
	}
	for _, kind := range info.Kinds {
		s.agents[kind] = agent
		s.infos[kind] = cloneInfo(info)
		if !s.knownEnabled[kind] {
			s.enabled[kind] = true
		}
	}
	return nil
}

func safeInfo(agent Agent) (info Info, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("decision agent Info panic: %v", value)
		}
	}()
	return agent.Info(), nil
}

func (s *Service) SetEnabled(kind core.DecisionKind, enabled bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	s.knownEnabled[kind] = true
	s.enabled[kind] = enabled
}

// SetLimits changes limits for subsequent calls. Non-positive values use defaults.
func (s *Service) SetLimits(maxPerRun, maxPerTask int) {
	if s == nil {
		return
	}
	if maxPerRun <= 0 {
		maxPerRun = DefaultMaxPerRun
	}
	if maxPerTask <= 0 {
		maxPerTask = DefaultMaxPerTask
	}
	s.mu.Lock()
	s.maxPerRun, s.maxPerTask = maxPerRun, maxPerTask
	s.mu.Unlock()
}

func (s *Service) SetTimeout(timeout time.Duration) {
	if s == nil {
		return
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	s.mu.Lock()
	s.timeout = timeout
	s.mu.Unlock()
}

func (s *Service) SetFailureThreshold(threshold int) {
	if s == nil {
		return
	}
	if threshold <= 0 {
		threshold = DefaultFailureThreshold
	}
	s.mu.Lock()
	s.failureThreshold = threshold
	s.mu.Unlock()
}

// Reset closes the circuit for kind and clears its consecutive failure count.
// Run and task budgets remain consumed.
func (s *Service) Reset(kind core.DecisionKind) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.ensureLocked()
	s.failures[kind] = 0
	s.open[kind] = false
	s.mu.Unlock()
}

func (s *Service) Decide(ctx context.Context, request core.DecisionRequest) Outcome {
	if s == nil {
		return Outcome{Status: StatusUnavailable, Reason: "decision service is unavailable"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request = cloneRequest(request)
	if err := request.Validate(); err != nil {
		return s.outcome(StatusInvalid, err.Error())
	}
	s.mu.Lock()
	s.ensureLocked()
	agent, registered := s.agents[request.Kind]
	info := cloneInfo(s.infos[request.Kind])
	switch {
	case !s.enabled[request.Kind]:
		s.mu.Unlock()
		return s.outcome(StatusDisabled, "decision kind is disabled")
	case !registered:
		s.mu.Unlock()
		return s.outcome(StatusUnavailable, "no agent is registered for decision kind")
	case s.open[request.Kind]:
		s.mu.Unlock()
		return s.outcome(StatusUnavailable, "decision circuit is open")
	case s.runCalls >= s.maxPerRun || s.taskCalls[request.TaskID] >= s.maxPerTask:
		s.mu.Unlock()
		return s.outcome(StatusBudgetExhausted, "decision call budget is exhausted")
	}
	s.runCalls++
	s.taskCalls[request.TaskID]++
	timeout := s.timeout
	threshold := s.failureThreshold
	s.mu.Unlock()
	if request.Budget.Timeout > 0 && request.Budget.Timeout < timeout {
		timeout = request.Budget.Timeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	type result struct {
		response core.DecisionResponse
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		response, err := safeDecide(agent, callCtx, request)
		resultCh <- result{response: response, err: err}
	}()
	select {
	case <-callCtx.Done():
		err := callCtx.Err()
		s.failed(request.Kind, threshold)
		if errors.Is(err, context.DeadlineExceeded) {
			return s.outcome(StatusError, "decision call timed out")
		}
		return s.outcome(StatusError, "decision call was canceled")
	case result := <-resultCh:
		if result.err != nil {
			s.failed(request.Kind, threshold)
			if IsInvalidOutput(result.err) {
				return s.outcome(StatusInvalid, result.err.Error())
			}
			return s.outcome(StatusError, result.err.Error())
		}
		result.response = cloneResponse(result.response)
		result.response.Meta.AgentID = firstNonEmpty(result.response.Meta.AgentID, info.ID)
		result.response.Meta.Version = firstNonEmpty(result.response.Meta.Version, info.Version)
		if result.response.Meta.Duration <= 0 {
			result.response.Meta.Duration = time.Since(started)
		}
		if err := result.response.Validate(request); err != nil {
			s.failed(request.Kind, threshold)
			return s.outcome(StatusInvalid, err.Error())
		}
		s.succeeded(request.Kind)
		return Outcome{Status: StatusRecommended, Response: &result.response}
	}
}

func (s *Service) ensureLocked() {
	if s.agents == nil {
		s.agents = map[core.DecisionKind]Agent{}
	}
	if s.infos == nil {
		s.infos = map[core.DecisionKind]Info{}
	}
	if s.enabled == nil {
		s.enabled = map[core.DecisionKind]bool{}
	}
	if s.knownEnabled == nil {
		s.knownEnabled = map[core.DecisionKind]bool{}
	}
	if s.failures == nil {
		s.failures = map[core.DecisionKind]int{}
	}
	if s.open == nil {
		s.open = map[core.DecisionKind]bool{}
	}
	if s.taskCalls == nil {
		s.taskCalls = map[core.TaskID]int{}
	}
	if s.timeout <= 0 {
		s.timeout = DefaultTimeout
	}
	if s.maxPerRun <= 0 {
		s.maxPerRun = DefaultMaxPerRun
	}
	if s.maxPerTask <= 0 {
		s.maxPerTask = DefaultMaxPerTask
	}
	if s.failureThreshold <= 0 {
		s.failureThreshold = DefaultFailureThreshold
	}
}

func safeDecide(agent Agent, ctx context.Context, request core.DecisionRequest) (response core.DecisionResponse, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("decision agent panic: %v", value)
		}
	}()
	return agent.Decide(ctx, request)
}

func (s *Service) failed(kind core.DecisionKind, threshold int) {
	s.mu.Lock()
	s.failures[kind]++
	if s.failures[kind] >= threshold {
		s.open[kind] = true
	}
	s.mu.Unlock()
}

func (s *Service) succeeded(kind core.DecisionKind) {
	s.mu.Lock()
	s.failures[kind] = 0
	s.mu.Unlock()
}

func (s *Service) outcome(status Status, reason string) Outcome {
	return Outcome{Status: status, Fallback: s.fallback, Reason: reason}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var ErrInvalidOutput = errors.New("invalid decision output")

func IsInvalidOutput(err error) bool { return errors.Is(err, ErrInvalidOutput) }

func invalidOutput(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOutput, fmt.Sprintf(format, args...))
}
