package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

type RecoveryReport struct {
	SkippedTailLines int
}

// EventLog is a small append-only JSON Lines file. It assigns sequence numbers
// before writing and restores them from valid records when opened.
type EventLog struct {
	mu         sync.Mutex
	file       *os.File
	writer     *bufio.Writer
	sequencer  *Sequencer
	syncWrites bool
	recovery   RecoveryReport
	closed     bool
	failed     error
}

// Open opens path. Set syncWrites to true to sync each appended record.
func Open(path string, syncWrites ...bool) (*EventLog, error) {
	if path == "" {
		return nil, fmt.Errorf("event log path is required")
	}
	if len(syncWrites) > 1 {
		return nil, fmt.Errorf("at most one syncWrites option is allowed")
	}
	syncEach := len(syncWrites) == 1 && syncWrites[0]
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	log := &EventLog{file: f, sequencer: NewSequencer(), syncWrites: syncEach}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	goodOffset, needsNewline, report, err := scanEventLog(bufio.NewReader(f), func(e Event) error {
		return log.sequencer.Restore(e.StreamKey(), e.Seq)
	})
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	log.recovery = report
	if report.SkippedTailLines > 0 {
		if err := f.Truncate(goodOffset); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	if needsNewline {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return nil, err
	}
	log.writer = bufio.NewWriter(f)
	return log, nil
}

func (l *EventLog) Append(e Event) (Event, error) {
	if l == nil {
		return Event{}, fmt.Errorf("nil event log")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer == nil || l.file == nil {
		return Event{}, fmt.Errorf("event log is not open")
	}
	if l.closed {
		return Event{}, os.ErrClosed
	}
	if l.failed != nil {
		return Event{}, l.failed
	}
	sequenced, err := l.sequencer.Assign(e)
	if err != nil {
		return Event{}, err
	}
	line, err := json.Marshal(sequenced)
	if err != nil {
		l.failed = err
		return Event{}, err
	}
	line = append(line, '\n')
	if _, err := l.writer.Write(line); err != nil {
		l.failed = err
		return Event{}, err
	}
	if err := l.writer.Flush(); err != nil {
		l.failed = err
		return Event{}, err
	}
	if l.syncWrites {
		if err := l.file.Sync(); err != nil {
			l.failed = err
			return Event{}, err
		}
	}
	return sequenced, nil
}

func (l *EventLog) Replay(visit func(Event) error) error {
	if l == nil {
		return fmt.Errorf("nil event log")
	}
	if visit == nil {
		return fmt.Errorf("replay callback is required")
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return os.ErrClosed
	}
	if l.writer == nil || l.file == nil {
		l.mu.Unlock()
		return fmt.Errorf("event log is not open")
	}
	if l.failed != nil {
		err := l.failed
		l.mu.Unlock()
		return err
	}
	if err := l.writer.Flush(); err != nil {
		l.failed = err
		l.mu.Unlock()
		return err
	}
	if _, err := l.file.Seek(0, io.SeekStart); err != nil {
		l.mu.Unlock()
		return err
	}
	var events []Event
	_, _, _, err := scanEventLog(bufio.NewReader(l.file), func(e Event) error {
		events = append(events, e)
		return nil
	})
	if _, seekErr := l.file.Seek(0, io.SeekEnd); err == nil {
		err = seekErr
	}
	l.mu.Unlock()
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := visit(event); err != nil {
			return err
		}
	}
	return nil
}

func (l *EventLog) Recovery() RecoveryReport {
	if l == nil {
		return RecoveryReport{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recovery
}

func (l *EventLog) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file == nil {
		return nil
	}
	flushErr := l.writer.Flush()
	if l.syncWrites && flushErr == nil {
		flushErr = l.file.Sync()
	}
	return errors.Join(flushErr, l.file.Close())
}

func scanEventLog(reader *bufio.Reader, visit func(Event) error) (goodOffset int64, needsNewline bool, report RecoveryReport, err error) {
	var offset int64
	last := map[string]uint64{}
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			return goodOffset, false, report, nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return goodOffset, false, report, readErr
		}
		terminated := len(line) > 0 && line[len(line)-1] == '\n'
		data := bytes.TrimSuffix(line, []byte{'\n'})
		data = bytes.TrimSuffix(data, []byte{'\r'})
		var event Event
		lineErr := json.Unmarshal(data, &event)
		if lineErr == nil {
			lineErr = event.Validate()
		}
		if lineErr == nil {
			stream := event.StreamKey()
			if event.Seq != last[stream]+1 {
				lineErr = fmt.Errorf("stream %q has sequence %d, expected %d", stream, event.Seq, last[stream]+1)
			} else {
				last[stream] = event.Seq
			}
		}
		if lineErr != nil {
			isTail := errors.Is(readErr, io.EOF)
			if terminated {
				_, peekErr := reader.Peek(1)
				isTail = errors.Is(peekErr, io.EOF)
			}
			if !isTail {
				return goodOffset, false, report, fmt.Errorf("corrupt event log before final line: %w", lineErr)
			}
			report.SkippedTailLines++
			return goodOffset, false, report, nil
		}
		if visit != nil {
			if err := visit(event); err != nil {
				return goodOffset, false, report, err
			}
		}
		offset += int64(len(line))
		goodOffset = offset
		if !terminated && errors.Is(readErr, io.EOF) {
			return goodOffset, true, report, nil
		}
		if errors.Is(readErr, io.EOF) {
			return goodOffset, false, report, nil
		}
	}
}
