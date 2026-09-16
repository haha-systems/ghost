package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/haha-systems/ghost/internal/event"
)

// maxMessageBytes bounds a message in a non-verbose trace. Whole file reads
// and command output would otherwise make ordinary traces enormous.
const maxMessageBytes = 512

type Writer struct {
	mu      sync.Mutex
	file    *os.File
	encoder *json.Encoder
	verbose bool
}

// Option configures a Writer.
type Option func(*Writer)

// Verbose records raw backend payloads and full messages. Without it the
// trace keeps summaries and identifies payloads by size and digest.
func Verbose(enabled bool) Option { return func(w *Writer) { w.verbose = enabled } }

func Open(path string, options ...Option) (*Writer, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	w := &Writer{file: file, encoder: json.NewEncoder(file)}
	for _, option := range options {
		option(w)
	}
	return w, nil
}

func (w *Writer) Write(e event.Event) error {
	if w == nil {
		return nil
	}
	meta := e.Metadata
	message := e.Message
	var raw json.RawMessage
	if !w.verbose && (len(e.Raw) > 0 || len(message) > maxMessageBytes) {
		meta = make(map[string]string, len(e.Metadata)+4)
		for key, value := range e.Metadata {
			meta[key] = value
		}
		if len(e.Raw) > 0 {
			meta["raw_bytes"] = strconv.Itoa(len(e.Raw))
			meta["raw_digest"] = digest(e.Raw)
		}
		if len(message) > maxMessageBytes {
			meta["message_bytes"] = strconv.Itoa(len(message))
			meta["message_digest"] = digest([]byte(message))
			message = truncate(message, maxMessageBytes)
		}
	} else if w.verbose && json.Valid(e.Raw) {
		raw = e.Raw
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.encoder.Encode(struct {
		Time      any               `json:"time"`
		Source    string            `json:"source"`
		Kind      event.Kind        `json:"kind"`
		Message   string            `json:"message"`
		SessionID string            `json:"session_id,omitempty"`
		TurnID    string            `json:"turn_id,omitempty"`
		Meta      map[string]string `json:"meta,omitempty"`
		Raw       json.RawMessage   `json:"raw,omitempty"`
	}{e.Time, e.Source, e.Kind, message, e.SessionID, e.TurnID, meta, raw})
}

func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	return w.file.Close()
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func truncate(s string, limit int) string {
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
