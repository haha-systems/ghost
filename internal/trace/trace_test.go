package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/haha-systems/ghost/internal/event"
)

func TestWriterAppendsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(event.Event{Source: "QAC", Kind: event.KindQAC, Message: "escalate"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "\"source\":\"QAC\"") {
		t.Fatalf("trace=%q err=%v", b, err)
	}
}

func writeOne(t *testing.T, e event.Event, options ...Option) map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	w, err := Open(path, options...)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(e); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWriterRecordsMetadataAndDigestsPayloadsByDefault(t *testing.T) {
	raw := []byte(`{"item":{"text":"` + strings.Repeat("x", 4096) + `"}}`)
	out := writeOne(t, event.Event{Source: "WRAITH", Kind: event.KindFile, Message: strings.Repeat("é", 600), Raw: raw, Metadata: map[string]string{"phase_run_id": "run_1"}})
	meta, _ := out["meta"].(map[string]any)
	if meta["phase_run_id"] != "run_1" || meta["raw_digest"] == nil || meta["raw_bytes"] != strconv.Itoa(len(raw)) || meta["message_bytes"] != "1200" {
		t.Fatalf("meta = %v", meta)
	}
	if _, ok := out["raw"]; ok {
		t.Fatal("raw payload written without verbose tracing")
	}
	if msg, _ := out["message"].(string); len(msg) > maxMessageBytes+len("…") || !utf8.ValidString(msg) {
		t.Fatalf("message not truncated cleanly: %d bytes", len(msg))
	}
}

func TestVerboseWriterKeepsPayloads(t *testing.T) {
	out := writeOne(t, event.Event{Source: "WRAITH", Message: strings.Repeat("a", 600), Raw: []byte(`{"a":1}`)}, Verbose(true))
	if out["raw"] == nil || len(out["message"].(string)) != 600 || out["meta"] != nil {
		t.Fatalf("verbose record = %v", out)
	}
}
