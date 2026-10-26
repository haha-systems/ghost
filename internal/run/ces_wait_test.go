package run

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/cognition"
	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/epistemic"
	"github.com/haha-systems/ghost/internal/runtime"
	"github.com/haha-systems/ghost/internal/runtime/fake"
	ghosttrace "github.com/haha-systems/ghost/internal/trace"
)

// manualTimer is an injectable AfterFunc whose timeout fires only on demand.
type manualTimer struct {
	mu       sync.Mutex
	fire     func()
	duration time.Duration
	stopped  bool
}

func (m *manualTimer) after(d time.Duration, f func()) func() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.duration, m.fire, m.stopped = d, f, false
	return func() bool { m.mu.Lock(); defer m.mu.Unlock(); m.stopped = true; return true }
}

func (m *manualTimer) state() (func(), time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fire, m.duration, m.stopped
}

type waitHarness struct {
	c      *Controller
	rt     *scriptRuntime
	events <-chan Event
	timer  *manualTimer
	wraith *fake.Session
	trace  string
}

// newWaitHarness runs TRIAGE on WRAITH while WRAITH's long-lived session is
// put into the given state, as Mark's run had a stopped WRAITH after TRIAGE.
func newWaitHarness(t *testing.T, mode string, prepare func(*fake.Session), scripts ...[]string) *waitHarness {
	t.Helper()
	return newGatedWaitHarness(t, mode, prepare, nil, scripts...)
}

func newGatedWaitHarness(t *testing.T, mode string, prepare func(*fake.Session), gates []<-chan struct{}, scripts ...[]string) *waitHarness {
	t.Helper()
	cfg := appQACConfig()
	cfg.QAC.OnUnavailable = mode
	h := &waitHarness{rt: &scriptRuntime{Runtime: fake.NewRuntime(), scripts: scripts, started: make(chan *scriptSession, 20)}, timer: &manualTimer{}, wraith: fake.NewSession("wraith")}
	// TRIAGE's start is held until prepare has run, so its scripted turn
	// cannot finish (and QAC cannot allocate) before WRAITH is in place.
	triage := make(chan struct{})
	h.rt.startGates = append([]<-chan struct{}{triage}, gates[min(1, len(gates)):]...)
	h.trace = filepath.Join(t.TempDir(), "trace.jsonl")
	w, err := ghosttrace.Open(h.trace)
	if err != nil {
		t.Fatal(err)
	}
	h.c = New(cfg, nil, Options{Runtime: h.rt, AfterFunc: h.timer.after, Trace: w})
	_ = h.c.SetSessions(map[string]runtime.Session{"wraith": h.wraith, "shade": fake.NewSession("shade")})
	events, unsubscribe := h.c.Subscribe()
	h.events = events
	t.Cleanup(func() {
		unsubscribe()
		if err := h.c.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := h.c.Start("make the sequence visible"); err != nil {
		t.Fatal(err)
	}
	<-h.rt.started // TRIAGE
	prepare(h.wraith)
	close(triage)
	return h
}

// waitFor reads events until one matches.
func (h *waitHarness) waitFor(t *testing.T, what string, match func(Event) bool) Event {
	t.Helper()
	for {
		if e := nextEvent(t, h.events); match(e) {
			return e
		} else if e.Boundary && e.Status != nil && e.Status.State.Task.ID != "" && !e.Status.Active {
			t.Fatalf("work ended (%s: %s) while waiting for %s", e.Status.State.Task.Status, e.Status.State.Task.TerminalReason, what)
		}
	}
}

func entryContains(text string) func(Event) bool {
	return func(e Event) bool { return strings.Contains(e.Entry.Message, text) }
}

func (h *waitHarness) phaseStarts() int {
	h.rt.mu.Lock()
	defer h.rt.mu.Unlock()
	n := 0
	for _, cfg := range h.rt.configs {
		if cfg.Ephemeral {
			n++
		}
	}
	return n
}

// traced reports whether the trace holds a CES event with this name.
func (h *waitHarness) traced(t *testing.T, name string) bool {
	t.Helper()
	data, err := os.ReadFile(h.trace)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(data), `"ces_event":"`+name+`"`)
}

func busy(s *fake.Session) { _ = s.Guard.StartTurn("roster-turn") }

func TestWaitModeWaitsForBusyResourceThenRunsNextPhase(t *testing.T) {
	h := newWaitHarness(t, config.QACUnavailableWait, busy, []string{chainArtifacts[0]}, []string{chainArtifacts[1]})
	h.waitFor(t, "wait status", entryContains("waiting for WRAITH (running) before ABDUCE"))
	if _, d, _ := h.timer.state(); d != config.DefaultUnavailableTimeout {
		t.Fatalf("timeout = %s", d)
	}
	if n := h.phaseStarts(); n != 1 {
		t.Fatalf("ABDUCE started while WRAITH was busy: %d phase sessions", n)
	}
	// An unrelated agent's event does not end the wait.
	_ = h.c.Observe(runtime.Event{AgentID: "shade", Kind: runtime.KindStatus})
	if n := h.phaseStarts(); n != 1 {
		t.Fatalf("phase sessions = %d", n)
	}
	h.wraith.Guard.Complete("roster-turn", false)
	_ = h.c.Observe(runtime.Event{AgentID: "wraith", Kind: runtime.KindStatus, Metadata: map[string]string{"backend_method": "turn/completed"}})
	h.waitFor(t, "ready status", entryContains("WRAITH is ready"))
	h.waitFor(t, "ABDUCE", entryContains("ABDUCE phase running on WRAITH"))
	if _, _, stopped := h.timer.state(); !stopped {
		t.Fatal("wait timer was not stopped")
	}
	for _, name := range []string{cognition.PhaseQACResourceWaiting, cognition.PhaseQACResourceReady} {
		if !h.traced(t, name) {
			t.Fatalf("trace is missing %s", name)
		}
	}
}

func TestWaitModeRestartsStoppedResourceOnce(t *testing.T) {
	stop := func(s *fake.Session) { s.Guard.Stop() }
	h := newWaitHarness(t, config.QACUnavailableWait, stop, []string{chainArtifacts[0]}, []string{}, []string{chainArtifacts[1]})
	h.waitFor(t, "restart status", entryContains("restarting WRAITH (stopped)"))
	restart := h.waitFor(t, "restarted session", func(e Event) bool { return e.RestartedSession != nil })
	if restart.RestartedSession.AgentID != "wraith" || restart.RestartedSession.Session.State() != runtime.StateIdle {
		t.Fatalf("restart = %#v", restart.RestartedSession)
	}
	h.waitFor(t, "ABDUCE", entryContains("ABDUCE phase running on WRAITH"))
	h.rt.mu.Lock()
	cfg := h.rt.configs[1]
	h.rt.mu.Unlock()
	if cfg.Ephemeral || cfg.AgentID != "wraith" || !strings.Contains(cfg.Instructions, "WRAITH") {
		t.Fatalf("restart config = ephemeral %v agent %q", cfg.Ephemeral, cfg.AgentID)
	}
	var installed runtime.Session
	_ = h.c.call(func(o *owner) { installed = o.sessions["wraith"] })
	if installed != restart.RestartedSession.Session {
		t.Fatal("owner did not install the restarted session")
	}
	if !h.traced(t, cognition.PhaseQACResourceRestart) {
		t.Fatal("trace is missing the restart")
	}
}

func TestWaitModeRestartsOnlyOnce(t *testing.T) {
	gate := make(chan struct{})
	stop := func(s *fake.Session) { s.Guard.Stop() }
	h := newGatedWaitHarness(t, config.QACUnavailableWait, stop, []<-chan struct{}{nil, gate}, []string{chainArtifacts[0]}, []string{}, []string{chainArtifacts[1]})
	restarted := <-h.rt.started
	restarted.Guard.Fail() // The restarted session fails before it is installed.
	close(gate)
	status, all := terminalEvents(t, h.events)
	if status.State.Task.TerminalReason != "cognitive_resource_unavailable" {
		t.Fatalf("task = %#v", status.State.Task)
	}
	found := false
	for _, e := range all {
		found = found || strings.Contains(e.Entry.Message, "WRAITH session is failed after a restart")
	}
	if !found || h.phaseStarts() != 1 || len(h.rt.StartConfigs()) != 2 {
		t.Fatalf("detail found %v, phase sessions %d, starts %d", found, h.phaseStarts(), len(h.rt.StartConfigs()))
	}
}

func TestWaitModeEndsWhenRestartFails(t *testing.T) {
	fail := func(s *fake.Session) { s.Guard.Fail() }
	// No script for the restart, so the runtime refuses to start it.
	h := newWaitHarness(t, config.QACUnavailableWait, fail, []string{chainArtifacts[0]})
	status, _ := terminalEvents(t, h.events)
	if status.State.Task.TerminalReason != "cognitive_resource_unavailable" {
		t.Fatalf("task = %#v", status.State.Task)
	}
	if n := h.phaseStarts(); n != 1 {
		t.Fatalf("phase sessions = %d", n)
	}
}

func TestWaitModeTimesOutAsResourceUnavailable(t *testing.T) {
	h := newWaitHarness(t, config.QACUnavailableWait, busy, []string{chainArtifacts[0]})
	h.waitFor(t, "wait status", entryContains("waiting for WRAITH"))
	fire, _, _ := h.timer.state()
	fire()
	status, all := terminalEvents(t, h.events)
	if status.State.Task.TerminalReason != "cognitive_resource_unavailable" {
		t.Fatalf("task = %#v", status.State.Task)
	}
	found := false
	for _, e := range all {
		found = found || strings.Contains(e.Entry.Message, "WRAITH was not ready within 5m0s (running)")
	}
	if !found {
		t.Fatal("timeout detail missing")
	}
}

func TestWaitModeCancelStopsTheWait(t *testing.T) {
	h := newWaitHarness(t, config.QACUnavailableWait, busy, []string{chainArtifacts[0]}, []string{chainArtifacts[1]})
	h.waitFor(t, "wait status", entryContains("waiting for WRAITH"))
	if err := h.c.Cancel(); err != nil {
		t.Fatal(err)
	}
	status, _ := terminalEvents(t, h.events)
	if status.State.Task.TerminalReason != "operator_canceled" {
		t.Fatalf("task = %#v", status.State.Task)
	}
	fire, _, stopped := h.timer.state()
	if !stopped {
		t.Fatal("wait timer was not stopped on cancel")
	}
	fire() // A late timer or session event changes nothing.
	h.wraith.Guard.Complete("roster-turn", false)
	_ = h.c.Observe(runtime.Event{AgentID: "wraith", Kind: runtime.KindStatus})
	if n := h.phaseStarts(); n != 1 {
		t.Fatalf("phase sessions after cancel = %d", n)
	}
	if got := h.c.Status().State.Task.TerminalReason; got != "operator_canceled" {
		t.Fatalf("terminal reason changed to %q", got)
	}
}

func TestStopModeEndsWhenTheResourceIsUnavailable(t *testing.T) {
	stop := func(s *fake.Session) { s.Guard.Stop() }
	h := newWaitHarness(t, config.QACUnavailableStop, stop, []string{chainArtifacts[0]}, []string{chainArtifacts[1]})
	status, _ := terminalEvents(t, h.events)
	if status.State.Task.Status != epistemic.WorkIncomplete || status.State.Task.TerminalReason != "cognitive_resource_unavailable" {
		t.Fatalf("task = %#v", status.State.Task)
	}
	if n := len(h.rt.StartConfigs()); n != 1 {
		t.Fatalf("stop mode started %d sessions", n)
	}
}

func TestRerouteModeMovesToTheNearestEligibleResource(t *testing.T) {
	stop := func(s *fake.Session) { s.Guard.Stop() }
	h := newWaitHarness(t, config.QACUnavailableReroute, stop, []string{chainArtifacts[0]}, []string{chainArtifacts[1]})
	h.waitFor(t, "ABDUCE on SHADE", entryContains("ABDUCE phase running on SHADE"))
}
