// Package epistemic renders the read-only CES overview and object inspector.
package epistemic

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/haha-systems/ghost/internal/event"
	"github.com/haha-systems/ghost/internal/ui/chrome"
	"github.com/haha-systems/ghost/internal/ui/footer"
	"github.com/haha-systems/ghost/internal/ui/keymap"
	"github.com/haha-systems/ghost/internal/ui/layout"
	"github.com/haha-systems/ghost/internal/ui/pane"
	"github.com/haha-systems/ghost/internal/ui/theme"
)

// BackMsg returns from the inspector overview to the dashboard.
type BackMsg struct{}

type itemRef struct {
	object   *Object
	relation *Relation
}

type Model struct {
	theme      theme.Theme
	keys       keymap.KeyMap
	snapshot   Snapshot
	content    pane.Pane
	selected   int
	itemRows   []int
	detail     bool
	trajectory []event.Event
	width      int
	height     int
}

const maxTrajectoryEvents = 200

// AppendEvent adds a semantic CES event to the read-only trajectory.
func (m Model) AppendEvent(item event.Event) Model {
	if !isCESEvent(item.Kind) {
		return m
	}
	m.trajectory = append(m.trajectory, cloneEvent(item))
	if len(m.trajectory) > maxTrajectoryEvents {
		m.trajectory = append([]event.Event(nil), m.trajectory[len(m.trajectory)-maxTrajectoryEvents:]...)
	}
	return m.rebuildContent()
}

func isCESEvent(kind event.Kind) bool {
	switch kind {
	case event.KindPhase, event.KindHypothesis, event.KindReject, event.KindFrame, event.KindAction,
		event.KindVerify, event.KindContradict, event.KindReopen, event.KindComplete, event.KindIncomplete:
		return true
	default:
		return false
	}
}

func cloneEvent(item event.Event) event.Event {
	if item.Metadata != nil {
		metadata := make(map[string]string, len(item.Metadata))
		for key, value := range item.Metadata {
			metadata[key] = value
		}
		item.Metadata = metadata
	}
	item.Raw = append([]byte(nil), item.Raw...)
	return item
}

func New(th theme.Theme, keys keymap.KeyMap) Model {
	return Model{theme: th, keys: keys, content: pane.New()}
}

// SetSnapshot replaces the presentation data while retaining selection,
// detail state, and scroll position where possible.
func (m Model) SetSnapshot(snapshot Snapshot) Model {
	previous := ""
	if items := m.items(); len(items) > 0 && m.selected < len(items) {
		previous = itemKey(items[m.selected])
	}
	m.snapshot = snapshot.clone()
	items := m.items()
	m.selected = 0
	for i, item := range items {
		if itemKey(item) == previous {
			m.selected = i
			break
		}
	}
	m = m.rebuildContent()
	if previous == "" && m.width >= layout.MinWidth && m.height >= layout.MinHeight {
		m.content.GotoTop()
	}
	return m
}

// SetSize adjusts the inspector to the current terminal dimensions.
func (m Model) SetSize(width, height int) Model {
	firstSize := m.width == 0 && m.height == 0
	m.width, m.height = max(width, 0), max(height, 0)
	if m.tooSmall() {
		return m
	}
	m.content.SetSize(max(width-4, 1), max(height-3, 1))
	if firstSize {
		m.content.GotoTop()
	}
	return m.rebuildContent()
}

func (m Model) DetailOpen() bool { return m.detail }

func (m Model) tooSmall() bool {
	return m.width < layout.MinWidth || m.height < layout.MinHeight
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		if m.tooSmall() {
			return m, nil
		}
		m.content.Scroll(pane.WheelDelta(wheel))
		return m, nil
	}
	keyMsg, isKey := msg.(tea.KeyPressMsg)
	if !isKey {
		return m, m.content.Update(msg)
	}
	if key.Matches(keyMsg, m.keys.Escape) {
		if m.detail {
			m.detail = false
			return m.rebuildContent(), nil
		}
		return m, func() tea.Msg { return BackMsg{} }
	}
	if m.detail {
		pane.HandleKey(&m.content, m.keys, keyMsg)
		return m, nil
	}
	switch {
	case key.Matches(keyMsg, m.keys.Up):
		m.moveSelection(-1)
		return m.rebuildContent(), nil
	case key.Matches(keyMsg, m.keys.Down):
		m.moveSelection(1)
		return m.rebuildContent(), nil
	case key.Matches(keyMsg, m.keys.Enter):
		if len(m.items()) > 0 {
			m.detail = true
			return m.rebuildContent(), nil
		}
		return m, nil
	default:
		pane.HandleKey(&m.content, m.keys, keyMsg)
		return m, nil
	}
}

func (m *Model) moveSelection(delta int) {
	count := len(m.items())
	if count == 0 {
		m.selected = 0
		return
	}
	m.selected = (m.selected + delta + count) % count
}

func (m Model) rebuildContent() Model {
	lines, itemRows := m.renderLines()
	m.itemRows = itemRows
	if m.width >= layout.MinWidth && m.height >= layout.MinHeight {
		width := max(m.width-4, 1)
		for i := range lines {
			lines[i] = chrome.Truncate(lines[i], width)
		}
		m.content.SetContent(joinLines(lines))
		if len(m.itemRows) > 0 && m.selected < len(m.itemRows) {
			m.ensureVisible(m.itemRows[m.selected])
		}
	} else {
		m.content.SetContent(joinLines(lines))
	}
	return m
}

func (m *Model) ensureVisible(row int) {
	// The current inspector keeps selection near the visible viewport when the
	// operator moves through a long object list.
	if row < 0 {
		return
	}
	offset := m.content.Offset()
	visibleRows := max(m.height-3, 1)
	if row < offset {
		m.content.ScrollUp(offset - row)
	} else if row >= offset+visibleRows {
		m.content.ScrollDown(row - offset - visibleRows + 1)
	}
}

func (m Model) View() string {
	if m.tooSmall() {
		return chrome.TooSmall(m.theme, m.width, m.height)
	}
	ctx := footer.Context{Screen: footer.Epistemic, Focus: footer.InspectorOverview}
	if m.detail {
		ctx.Focus = footer.InspectorDetail
	}
	body := m.content.View() + "\n" + footer.Render(m.theme, footer.Hints(ctx), m.width-4)
	return theme.Frame(m.theme, m.width, m.height, body)
}

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
