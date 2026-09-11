package trace

import (
	"github.com/haha-systems/ghost/internal/event"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
