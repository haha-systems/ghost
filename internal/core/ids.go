package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

var ErrInvalidID = errors.New("invalid ID")

// AgentID identifies an agent contract.
type AgentID string

// TaskID identifies a runtime task.
type TaskID string

// MessageID identifies a communication message.
type MessageID string

// EventID identifies an event envelope.
type EventID string

// ThreadID identifies a conversation thread.
type ThreadID string
type ConsultationID string

// SessionID identifies one provider session.
type SessionID string

// WorkspaceID identifies a Ghost workspace.
type WorkspaceID string

// HumanAgentID is the reserved operator identity used as a task requester,
// message sender, or message recipient. It is not a runtime agent ID.
const HumanAgentID AgentID = "you"

// IDGenerator makes sortable IDs. Clock and entropy are injectable for tests.
// A single generator keeps the time portion increasing when its clock repeats
// or moves backwards.
type IDGenerator struct {
	mu      sync.Mutex
	clock   func() time.Time
	entropy io.Reader
	lastNS  int64
	used    map[string]struct{}
}

// NewIDGenerator returns a generator that uses the supplied clock and entropy.
// Nil inputs use the system clock and crypto/rand.Reader.
func NewIDGenerator(clock func() time.Time, entropy io.Reader) *IDGenerator {
	if clock == nil {
		clock = time.Now
	}
	if entropy == nil {
		entropy = rand.Reader
	}
	return &IDGenerator{clock: clock, entropy: entropy, lastNS: -1}
}

var defaultIDGenerator = NewIDGenerator(time.Now, rand.Reader)

// NewID creates prefix_<unix-nanoseconds-hex><random-hex>.
func NewID(prefix string) (string, error) { return defaultIDGenerator.New(prefix) }

// New creates one sortable ID with the given lower-case prefix.
func (g *IDGenerator) New(prefix string) (string, error) {
	if !validPrefix(prefix) {
		return "", fmt.Errorf("%w: invalid prefix %q", ErrInvalidID, prefix)
	}
	if g == nil {
		return "", errors.New("nil ID generator")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	ns := g.clock().UnixNano()
	if ns < 0 {
		return "", fmt.Errorf("%w: clock is before unix epoch", ErrInvalidID)
	}
	for {
		if ns <= g.lastNS {
			if g.lastNS == int64(^uint64(0)>>1) {
				return "", errors.New("ID timestamp exhausted")
			}
			ns = g.lastNS + 1
		}
		var suffix [8]byte
		if _, err := io.ReadFull(g.entropy, suffix[:]); err != nil {
			return "", fmt.Errorf("read ID entropy: %w", err)
		}
		id := fmt.Sprintf("%s_%016x%s", prefix, uint64(ns), hex.EncodeToString(suffix[:]))
		g.lastNS = ns
		if _, exists := g.used[id]; exists {
			ns++
			continue
		}
		if g.used == nil {
			g.used = map[string]struct{}{}
		}
		g.used[id] = struct{}{}
		return id, nil
	}
}

// Reserve prevents a generated ID from matching a value read from durable
// state. It is safe to call before or during generation.
func (g *IDGenerator) Reserve(id string) error {
	if g == nil {
		return errors.New("nil ID generator")
	}
	prefix, _, ok := strings.Cut(id, "_")
	if !ok || validateID(prefix, id) != nil {
		return fmt.Errorf("%w: malformed reserved ID", ErrInvalidID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.used == nil {
		g.used = map[string]struct{}{}
	}
	g.used[id] = struct{}{}
	return nil
}

func validPrefix(prefix string) bool {
	if prefix == "" || prefix[0] < 'a' || prefix[0] > 'z' {
		return false
	}
	for _, ch := range prefix[1:] {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return false
		}
	}
	return true
}

func validateID(prefix, value string) error {
	gotPrefix, suffix, ok := strings.Cut(value, "_")
	if !ok || gotPrefix != prefix || len(suffix) != 32 {
		return fmt.Errorf("%w: expected %s ID", ErrInvalidID, prefix)
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		return fmt.Errorf("%w: malformed %s ID", ErrInvalidID, prefix)
	}
	return nil
}

func (id AgentID) Validate() error        { return validateID("agent", string(id)) }
func (id TaskID) Validate() error         { return validateID("task", string(id)) }
func (id MessageID) Validate() error      { return validateID("message", string(id)) }
func (id EventID) Validate() error        { return validateID("event", string(id)) }
func (id ThreadID) Validate() error       { return validateID("thread", string(id)) }
func (id ConsultationID) Validate() error { return validateID("consultation", string(id)) }
func (id SessionID) Validate() error      { return validateID("session", string(id)) }
func (id WorkspaceID) Validate() error    { return validateID("workspace", string(id)) }
