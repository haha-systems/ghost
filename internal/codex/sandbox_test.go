package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/haha-systems/ghost/internal/runtime"
)

func TestStartThreadSandboxParam(t *testing.T) {
	cases := []struct {
		mode runtime.SandboxMode
		want any
	}{
		{runtime.SandboxReadOnly, "read-only"},
		{runtime.SandboxEnabled, "workspace-write"},
		{runtime.SandboxDisabled, "danger-full-access"},
		{runtime.SandboxDefault, nil},
	}
	for _, tc := range cases {
		serverOut, clientIn := io.Pipe()
		serverIn, clientOut := io.Pipe()
		c := newRPC(clientOut, serverOut)
		params := make(chan map[string]any, 1)
		go func() {
			var got struct {
				ID     int64          `json:"id"`
				Params map[string]any `json:"params"`
			}
			_ = json.NewDecoder(serverIn).Decode(&got)
			params <- got.Params
			_, _ = fmt.Fprintf(clientIn, `{"jsonrpc":"2.0","id":%d,"result":{"thread":{"id":"thread-1"}}}`+"\n", got.ID)
		}()
		if _, err := startThread(t.Context(), c, runtime.SessionConfig{AgentID: "veil", WorkingDir: "/work", Sandbox: tc.mode}); err != nil {
			t.Fatal(err)
		}
		got := <-params
		value, ok := got["sandbox"]
		if tc.want == nil {
			if ok {
				t.Fatalf("mode %v: sandbox param sent: %#v", tc.mode, value)
			}
			continue
		}
		if value != tc.want {
			t.Fatalf("mode %v: sandbox = %#v, want %v", tc.mode, value, tc.want)
		}
		_ = clientIn.Close()
		_ = serverIn.Close()
	}
}
