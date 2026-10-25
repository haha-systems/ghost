package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/runtime/fake"
)

func testConfig(t *testing.T) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	agents := config.AgentSet{"wraith": {Runtime: "fake", WorkingDir: dir}, "shade": {Runtime: "fake", WorkingDir: dir}}
	cfg := config.Default()
	cfg.Agents = &agents
	cfg.QAC = config.QACConfig{
		Enabled: true, EntryResource: "wraith", DefaultImportance: .5,
		Policy:    config.QACPolicyConfig{Type: "threshold", Hierarchy: []string{"wraith", "shade"}},
		Resources: map[string]config.QACResourceConfig{"wraith": {Agent: "wraith", Capability: .3, Cost: .1}, "shade": {Agent: "shade", Capability: .6, Cost: .3}},
	}
	return cfg, dir
}

func TestRequestKeyIsIdempotentAndConflictsWhenReused(t *testing.T) {
	cfg, dir := testConfig(t)
	server, err := New(cfg, nil, WithRuntime(fake.NewRuntime()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	body := func(key, task string) json.RawMessage {
		raw, _ := json.Marshal(map[string]string{"workspace": dir, "task": task, "actor": "shade", "request_key": key})
		return raw
	}
	first, rpcErr := server.start(body("k1", "trace the boundary"))
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	second, rpcErr := server.start(body("k1", "trace the boundary"))
	if rpcErr != nil || second.(map[string]any)["run_id"] != first.(map[string]any)["run_id"] {
		t.Fatalf("repeat = %#v err=%v", second, rpcErr)
	}
	if _, rpcErr = server.start(body("k1", "different task")); rpcErr == nil || rpcErr.Code != codeConflict {
		t.Fatalf("reused key err = %#v", rpcErr)
	}
	status, rpcErr := server.status(must(json.Marshal(map[string]string{"run_id": first.(map[string]any)["run_id"].(string)})))
	if rpcErr != nil || status.(map[string]any)["active"] != true {
		t.Fatalf("status=%#v err=%v", status, rpcErr)
	}
	if _, rpcErr = server.override(must(json.Marshal(map[string]string{"run_id": first.(map[string]any)["run_id"].(string), "resource_id": "shade", "reason": "look higher", "request_key": "ov1"}))); rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	approval, err := server.controller.RequestApproval("shade", "shell", "verify", []string{"go", "test"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]string{"approval_id": approval.ID, "decision": "approve", "responder": "mark", "request_key": "ap1"}
	got, rpcErr := server.respond(must(json.Marshal(params)))
	if rpcErr != nil || got.(map[string]any)["decision"] != "approve" {
		t.Fatalf("respond=%#v err=%v", got, rpcErr)
	}
	again, rpcErr := server.respond(must(json.Marshal(params)))
	if rpcErr != nil || again.(map[string]any)["decision"] != "approve" {
		t.Fatalf("repeat respond=%#v err=%v", again, rpcErr)
	}
	params["decision"] = "deny"
	params["request_key"] = "ap2"
	if _, rpcErr = server.respond(must(json.Marshal(params))); rpcErr == nil || rpcErr.Code != codeConflict {
		t.Fatalf("conflicting decision err=%#v", rpcErr)
	}
	canceled, rpcErr := server.cancel(must(json.Marshal(map[string]string{"run_id": first.(map[string]any)["run_id"].(string), "request_key": "cancel-1", "reason": "stop"})))
	if rpcErr != nil || canceled.(map[string]any)["state"] == "active" {
		t.Fatalf("cancel=%#v err=%v", canceled, rpcErr)
	}
}

func TestStdioRejectsAMutationNotification(t *testing.T) {
	cfg, dir := testConfig(t)
	server, err := New(cfg, nil, WithRuntime(fake.NewRuntime()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	var out bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"run.start","params":{"workspace":"` + dir + `","task":"nope","actor":"shade","request_key":"n1"}}` + "\n")
	if err := server.Serve(t.Context(), in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("notification produced %s", out.String())
	}
	if server.controller.Status().Active {
		t.Fatal("notification started a run")
	}
}

func TestSocketRejectsASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.sock")
	link := filepath.Join(dir, "link.sock")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenSocket(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink socket err = %v", err)
	}
}

func TestSocketAcceptsTheSameUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ghost.sock")
	listener, err := ListenSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := AcceptSameUser(listener)
		if err == nil {
			_, _ = io.WriteString(conn, "ok\n")
			_ = conn.Close()
		}
		done <- err
	}()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "ok\n" {
		t.Fatalf("line=%q err=%v", line, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func must(raw []byte, err error) json.RawMessage {
	if err != nil {
		panic(err)
	}
	return raw
}
