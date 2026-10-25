//go:build live

package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/runtime"
)

// TestLiveCodexAppServerReadOnlySession starts one real Codex App Server
// session in a temporary workspace with the read-only sandbox and sends one
// short prompt. It needs the codex binary and a logged-in Codex account, and
// uses a small amount of Codex quota, so it runs only with `-tags live` and
// GHOST_LIVE=1. CI never sets either.
func TestLiveCodexAppServerReadOnlySession(t *testing.T) {
	if os.Getenv("GHOST_LIVE") != "1" {
		t.Skip("live smoke test: set GHOST_LIVE=1 (and -tags live) to run against the real Codex App Server")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ghost live smoke workspace\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	effort := os.Getenv("GHOST_LIVE_CODEX_EFFORT")
	if effort == "" {
		effort = "low"
	}
	r := NewRuntime()
	defer func() { _ = r.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	started := time.Now()
	session, err := r.Start(ctx, runtime.SessionConfig{
		AgentID: "live-smoke", WorkingDir: dir, Model: os.Getenv("GHOST_LIVE_CODEX_MODEL"), Effort: effort,
		Instructions: "You are a smoke test. Answer briefly. Do not run commands or edit files.",
		Sandbox:      runtime.SandboxReadOnly, Approvals: runtime.ApprovalNever,
	})
	if err != nil {
		t.Fatalf("start live Codex session: %v", err)
	}
	defer func() { _ = session.Close() }()
	if err := session.Send(ctx, runtime.Input{Text: "Reply with exactly the single word PONG."}); err != nil {
		t.Fatalf("send: %v", err)
	}
	kinds := map[runtime.EventKind]int{}
	var text strings.Builder
	var usage []string
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("turn did not complete: kinds=%v state=%s", kinds, session.State())
		case e, ok := <-session.Events():
			if !ok {
				t.Fatalf("event stream closed early: kinds=%v", kinds)
			}
			kinds[e.Kind]++
			// Deltas and the completed item both carry the reply text; keep
			// only the completed agent message so the reply is not doubled.
			if e.Kind == runtime.KindMessage && e.Metadata["backend_method"] == "item/completed" {
				text.WriteString(e.Summary)
			}
			if e.Kind == runtime.KindUsage {
				// The adapter summary is only the method name; keep the raw
				// payload so the run records the token counts it cost.
				usage = append(usage, string(e.Raw))
			}
			method := e.Metadata["backend_method"]
			if method == "turn/failed" {
				t.Fatalf("turn failed: %s %s", e.Summary, e.Detail)
			}
			if method != "turn/completed" {
				continue
			}
			t.Logf("model=%q thread=%q elapsed=%s events=%v usage=%q", session.Metadata().Model, session.Metadata().ThreadID, time.Since(started).Round(time.Millisecond), kinds, usage)
			t.Logf("reply=%q", strings.TrimSpace(text.String()))
			if kinds[runtime.KindMessage] == 0 {
				t.Fatalf("no agent message events: %v", kinds)
			}
			if kinds[runtime.KindCommand] != 0 || kinds[runtime.KindFile] != 0 {
				t.Logf("note: the model emitted command/file events despite the prompt: %v", kinds)
			}
			deadline := time.Now().Add(5 * time.Second)
			for session.State() != runtime.StateIdle && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if session.State() != runtime.StateIdle {
				t.Fatalf("session state after completion = %s", session.State())
			}
			return
		}
	}
}
