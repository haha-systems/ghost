package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/qac"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime"
)

// fakeAppServer is an in-memory Codex App Server. It answers initialize,
// thread/start and thread/unsubscribe, and can broadcast an unaddressed
// notification the way account/rateLimits/updated arrives in production.
type fakeAppServer struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	writeMu sync.Mutex
	done    chan struct{}
	once    sync.Once
	threads int
}

func newFakeAppServer() *fakeAppServer {
	s := &fakeAppServer{done: make(chan struct{})}
	s.stdinR, s.stdinW = io.Pipe()
	s.stdoutR, s.stdoutW = io.Pipe()
	return s
}

func (s *fakeAppServer) StdinPipe() (io.WriteCloser, error) { return s.stdinW, nil }
func (s *fakeAppServer) StdoutPipe() (io.ReadCloser, error) { return s.stdoutR, nil }
func (s *fakeAppServer) StderrPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (s *fakeAppServer) Wait() error { <-s.done; return nil }
func (s *fakeAppServer) Kill() error {
	s.once.Do(func() {
		close(s.done)
		_ = s.stdinR.Close()
		_ = s.stdoutW.Close()
	})
	return nil
}

func (s *fakeAppServer) Start() error {
	go func() {
		decoder := json.NewDecoder(s.stdinR)
		for {
			var request struct {
				ID     *int64 `json:"id"`
				Method string `json:"method"`
			}
			if err := decoder.Decode(&request); err != nil {
				return
			}
			if request.ID == nil {
				continue
			}
			result := `{}`
			if request.Method == "thread/start" {
				s.threads++
				result = fmt.Sprintf(`{"thread":{"id":"thread-%d"}}`, s.threads)
			}
			s.write(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, *request.ID, result))
		}
	}()
	return nil
}

func (s *fakeAppServer) write(line string) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = io.WriteString(s.stdoutW, line+"\n")
}

func (s *fakeAppServer) broadcastRateLimits() {
	s.write(`{"jsonrpc":"2.0","method":"account/rateLimits/updated","params":{"rateLimits":{"primary":{"usedPercent":1}}}}`)
}

func fakeCodexRuntime(t *testing.T) (*Runtime, *fakeAppServer) {
	t.Helper()
	server := newFakeAppServer()
	oldFactory, oldLookPath := processFactory, codexLookPath
	processFactory = func(context.Context, string) process { return server }
	codexLookPath = func(string) (string, error) { return "codex", nil }
	r := NewRuntime()
	t.Cleanup(func() {
		_ = r.Close()
		processFactory, codexLookPath = oldFactory, oldLookPath
	})
	return r, server
}

func waitForBackendMethod(t *testing.T, s runtime.Session, method string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatalf("%s events closed before %s", s.ID(), method)
			}
			if e.Metadata["backend_method"] == method {
				return
			}
		case <-deadline:
			t.Fatalf("%s did not receive %s", s.ID(), method)
		}
	}
}

// TestPhaseSessionKeepsTheLongLivedSessionAvailable replays the 26 October
// cognitive_resource_unavailable run against the real runtime: the roster
// starts VEIL, SHADE and WRAITH, CES runs TRIAGE in a fresh WRAITH phase
// session, and the ABDUCE allocation (direction=release) reads the roster
// sessions. Starting the phase used to close WRAITH's roster session, so QAC
// saw WRAITH as unavailable and stopped the work.
func TestPhaseSessionKeepsTheLongLivedSessionAvailable(t *testing.T) {
	r, server := fakeCodexRuntime(t)
	ctx := t.Context()
	roster := map[string]runtime.Session{}
	for _, agent := range []string{"veil", "shade", "wraith"} {
		s, err := r.Start(ctx, runtime.SessionConfig{AgentID: agent})
		if err != nil {
			t.Fatal(err)
		}
		roster[agent] = s
	}

	triage, err := r.Start(ctx, runtime.SessionConfig{AgentID: "wraith", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := roster["wraith"].State(); got != runtime.StateIdle {
		t.Errorf("roster wraith state after the TRIAGE phase started = %s, want idle", got)
	}

	// Unaddressed broadcasts still reach the roster thread, not only the phase.
	server.broadcastRateLimits()
	waitForBackendMethod(t, roster["wraith"], "account/rateLimits/updated")

	if err := triage.Close(); err != nil {
		t.Fatal(err)
	}
	if got := roster["wraith"].State(); got != runtime.StateIdle {
		t.Errorf("roster wraith state after the TRIAGE phase closed = %s, want idle", got)
	}

	c, err := cognition.New(config.QACConfig{
		Enabled: true, EntryResource: "wraith", DefaultImportance: .5,
		Policy: config.QACPolicyConfig{Type: "threshold", Hierarchy: []string{"wraith", "shade", "veil"}},
		Resources: map[string]config.QACResourceConfig{
			"wraith": {Agent: "wraith", Capability: .30, Cost: .10, Scarcity: .05},
			"shade":  {Agent: "shade", Capability: .65, Cost: .30, Scarcity: .30},
			"veil":   {Agent: "veil", Capability: .95, Cost: .80, Scarcity: .95},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	start, err := c.StartWork("simple task")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Commit(start, time.Now()); err != nil {
		t.Fatal(err)
	}
	request := cognition.QACRequest{Direction: "release", Uncertainty: .68, Novelty: .10, ExpectedGain: .80, Reason: "phase transition"}
	plan, err := c.AllocateWithSituation(context.Background(), request, cognition.CognitiveSituation{}, roster, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action == qac.ActionStop {
		t.Fatalf("ABDUCE allocation stopped: %q eligibility %+v", plan.Decision.Reason, plan.Decision.Eligibility)
	}
	for _, item := range plan.Decision.Eligibility {
		if item.ResourceID == "wraith" && !item.Eligible {
			t.Fatalf("wraith ineligible: %v", item.Reasons)
		}
	}
}

// TestPhaseSessionsStillReplaceEachOther keeps the bb415d18 guarantee: one
// agent never holds two phase threads at once.
func TestPhaseSessionsStillReplaceEachOther(t *testing.T) {
	r, _ := fakeCodexRuntime(t)
	ctx := t.Context()
	roster, err := r.Start(ctx, runtime.SessionConfig{AgentID: "wraith"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Start(ctx, runtime.SessionConfig{AgentID: "wraith", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Start(ctx, runtime.SessionConfig{AgentID: "wraith", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.State() != runtime.StateStopped {
		t.Fatalf("first phase state = %s, want stopped", first.State())
	}
	if second.State() != runtime.StateIdle || roster.State() != runtime.StateIdle {
		t.Fatalf("second phase = %s roster = %s, want both idle", second.State(), roster.State())
	}

	// Restarting the roster replaces the previous long-lived session only.
	replacement, err := r.Start(ctx, runtime.SessionConfig{AgentID: "wraith"})
	if err != nil {
		t.Fatal(err)
	}
	if roster.State() != runtime.StateStopped || replacement.State() != runtime.StateIdle || second.State() != runtime.StateIdle {
		t.Fatalf("roster=%s replacement=%s phase=%s", roster.State(), replacement.State(), second.State())
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if second.State() != runtime.StateStopped || replacement.State() != runtime.StateStopped {
		t.Fatalf("after Close phase=%s roster=%s, want both stopped", second.State(), replacement.State())
	}
}
