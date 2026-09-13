package history

import (
	"bytes"

	"github.com/haha-systems/ghost/internal/event"
)

// DefaultLimit bounds the retained run. The disk trace remains the complete
// record of a run; this store only has to hold what an operator can still
// usefully scroll back through.
const DefaultLimit = 5000

// Store is the canonical, bounded history of a run.
//
// Per-agent views are projected from the single global history rather than
// maintained as separate slices, so an entry that belongs to two agents (a QAC
// handoff) is stored once and cannot drift between them.
type Store struct {
	limit   int
	entries []Entry
	// start is the index of the oldest live entry. Dropping an entry advances
	// this offset instead of copying the whole slice, and the tail is compacted
	// only once the dead prefix has grown large.
	start int
}

// New returns a store bounded to limit entries. A limit below one falls back to
// DefaultLimit so a zero value cannot silently discard everything.
func New(limit int) *Store {
	if limit < 1 {
		limit = DefaultLimit
	}
	return &Store{limit: limit}
}

// Append records an entry and returns the entry as stored.
//
// Streamed responses arrive as many small fragments for one logical message.
// A fragment that continues the previous response from the same agent, session,
// and turn extends it in place, so the operator sees one evolving message
// rather than a column of syllables. The second return reports that merge, so a
// projection can update its last row instead of adding another.
func (s *Store) Append(e Entry) (Entry, bool) {
	if e.Kind == event.KindResponse {
		if last := s.last(); last != nil && s.continues(*last, e) {
			// An exact repeat is a re-delivery of the same fragment, not new text.
			if last.Message == e.Message && bytes.Equal(last.Raw, e.Raw) {
				return last.clone(), true
			}
			last.Message += e.Message
			last.Raw = e.Raw
			return last.clone(), true
		}
	}
	s.entries = append(s.entries, e.clone())
	if len(s.entries)-s.start > s.limit {
		s.start++
	}
	if s.start > s.limit {
		s.entries = append([]Entry(nil), s.entries[s.start:]...)
		s.start = 0
	}
	return e.clone(), false
}

// ReplaceLatest rewrites the message of the most recent response from an agent.
// QAC rewrites a response once it has parsed its request block out of it;
// without this the operator would keep reading the unredacted text.
func (s *Store) ReplaceLatest(agentID string, kind event.Kind, message string) (Entry, bool) {
	live := s.entries[s.start:]
	for i := len(live) - 1; i >= 0; i-- {
		if live[i].AgentID == agentID && live[i].Kind == kind {
			live[i].Message = message
			return live[i].clone(), true
		}
	}
	return Entry{}, false
}

// Agent returns the entries belonging to one agent, oldest first.
func (s *Store) Agent(agentID string) []Entry {
	live := s.entries[s.start:]
	out := make([]Entry, 0, len(live))
	for _, e := range live {
		if Belongs(e, agentID) {
			out = append(out, e.clone())
		}
	}
	return out
}

// All returns the whole retained history, oldest first.
func (s *Store) All() []Entry {
	live := s.entries[s.start:]
	out := make([]Entry, 0, len(live))
	for _, e := range live {
		out = append(out, e.clone())
	}
	return out
}

// Len reports how many entries are retained.
func (s *Store) Len() int { return len(s.entries) - s.start }

func (s *Store) last() *Entry {
	if len(s.entries) == s.start {
		return nil
	}
	return &s.entries[len(s.entries)-1]
}

// continues reports whether b is a further fragment of the streamed response a.
func (s *Store) continues(a, b Entry) bool {
	if a.Kind != event.KindResponse || a.AgentID != b.AgentID {
		return false
	}
	if a.SessionID == "" || b.SessionID == "" || a.TurnID == "" || b.TurnID == "" {
		// Without scope identifiers the only safe join is between two events
		// that are both unscoped; otherwise unrelated turns would merge.
		return a.SessionID == "" && b.SessionID == "" && a.TurnID == "" && b.TurnID == ""
	}
	return a.SessionID == b.SessionID && a.TurnID == b.TurnID
}
