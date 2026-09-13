// Package pane couples a scrolling viewport with the follow semantics every
// live log in Ghost needs.
//
// Live output must never fight the operator: scrolling away from the bottom
// suspends auto-follow, entries that arrive while detached are counted rather
// than allowed to drag the viewport down, and reaching the bottom again resumes
// following. Each pane owns this state independently, so reading back through
// one pane's history does not stop another from following.
package pane

import (
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// Pane is one scrolling log pane.
type Pane struct {
	viewport viewport.Model
	follow   bool
	newCount int
}

// New returns a pane that starts at the bottom, following live output.
func New() Pane {
	return Pane{
		viewport: viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		follow:   true,
	}
}

// Follow reports whether the pane is pinned to live output.
func (p Pane) Follow() bool { return p.follow }

// NewCount reports how many entries have arrived since follow was suspended.
func (p Pane) NewCount() int { return p.newCount }

// View renders the pane's visible window.
func (p Pane) View() string { return p.viewport.View() }

// SetSize resizes the pane, keeping at least one usable row and column.
func (p *Pane) SetSize(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	p.viewport.SetWidth(width)
	p.viewport.SetHeight(height)
	if p.follow {
		p.viewport.GotoBottom()
	}
}

// SetContent replaces the rendered content.
//
// While detached the scroll offset is restored afterwards, so a re-render
// triggered by unrelated traffic — a metadata update, a QAC transition, a tick
// — cannot move the operator's position in the history they are reading.
func (p *Pane) SetContent(content string) {
	offset := p.viewport.YOffset()
	p.viewport.SetContent(content)
	if p.follow {
		p.viewport.GotoBottom()
		return
	}
	p.viewport.SetYOffset(offset)
}

// Noted records that n entries were appended. Following panes stay at the
// bottom; detached panes count them for the new-entry indicator instead.
func (p *Pane) Noted(n int) {
	if n <= 0 {
		return
	}
	if p.follow {
		p.viewport.GotoBottom()
		return
	}
	p.newCount += n
}

// Reset returns the pane to the bottom, following, with nothing outstanding.
// Opening a different agent starts from live output rather than inheriting the
// position of whatever was on screen before.
func (p *Pane) Reset() {
	p.follow = true
	p.newCount = 0
	p.viewport.GotoBottom()
}

// GotoBottom resumes following and clears the indicator, which is what End does.
func (p *Pane) GotoBottom() {
	p.viewport.GotoBottom()
	p.sync()
}

// GotoTop jumps to the oldest content, which necessarily suspends follow.
func (p *Pane) GotoTop() {
	p.viewport.GotoTop()
	p.sync()
}

// ScrollUp and ScrollDown move by whole rows.
func (p *Pane) ScrollUp(n int)   { p.viewport.ScrollUp(n); p.sync() }
func (p *Pane) ScrollDown(n int) { p.viewport.ScrollDown(n); p.sync() }

// PageUp and PageDown move by a whole screen.
func (p *Pane) PageUp()   { p.viewport.PageUp(); p.sync() }
func (p *Pane) PageDown() { p.viewport.PageDown(); p.sync() }

// Update routes a message to the viewport, re-deriving follow from where the
// viewport ends up. Mouse wheel events reach a pane this way.
func (p *Pane) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	p.sync()
	return cmd
}

// sync derives follow from position: at the bottom is following, and anywhere
// else is not. Arriving at the bottom by any route clears the indicator.
func (p *Pane) sync() {
	p.follow = p.viewport.AtBottom()
	if p.follow {
		p.newCount = 0
	}
}
