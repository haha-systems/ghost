package trace

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/haha-systems/ghost/internal/event"
)

type Writer struct {
	mu      sync.Mutex
	file    *os.File
	encoder *json.Encoder
}

func Open(path string) (*Writer, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Writer{file: file, encoder: json.NewEncoder(file)}, nil
}
func (w *Writer) Write(e event.Event) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.encoder.Encode(struct {
		Time      any             `json:"time"`
		Source    string          `json:"source"`
		Kind      event.Kind      `json:"kind"`
		Message   string          `json:"message"`
		SessionID string          `json:"session_id,omitempty"`
		TurnID    string          `json:"turn_id,omitempty"`
		Raw       json.RawMessage `json:"raw,omitempty"`
	}{e.Time, e.Source, e.Kind, e.Message, e.SessionID, e.TurnID, e.Raw})
}
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	return w.file.Close()
}
