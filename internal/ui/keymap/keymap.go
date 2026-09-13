// Package keymap defines the application's shared keyboard contract.
package keymap

import (
	"charm.land/bubbles/v2/key"
)

// KeyMap contains navigation, focus, help, and quit bindings used by every
// screen. Text inputs may consume ordinary characters before the root model
// applies these bindings.
type KeyMap struct {
	Up        key.Binding
	Down      key.Binding
	Enter     key.Binding
	Escape    key.Binding
	Tab       key.Binding
	ShiftTab  key.Binding
	Help      key.Binding
	Quit      key.Binding
	ForceQuit key.Binding
	Interrupt key.Binding

	// Scrolling bindings apply to whichever log pane holds focus.
	PageUp   key.Binding
	PageDown key.Binding
	Home     key.Binding
	End      key.Binding

	// These aliases make intent clear at call sites that use semantic names.
	Previous  key.Binding
	Next      key.Binding
	Open      key.Binding
	Back      key.Binding
	FocusNext key.Binding
	FocusPrev key.Binding
}

// Default returns the bindings specified by the Phase 0 keyboard contract.
func Default() KeyMap {
	up := key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "previous agent"))
	down := key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "next agent"))
	enter := key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open selected agent"))
	escape := key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "return to dashboard"))
	tab := key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "change focus"))
	help := key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "show key help"))
	quit := key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
	forceQuit := key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "force quit"))
	interrupt := key.NewBinding(key.WithKeys("ctrl+x"), key.WithHelp("ctrl+x", "interrupt turn"))
	shiftTab := key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "previous focus"))
	pageUp := key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up"))
	pageDown := key.NewBinding(key.WithKeys("pgdown", "pgdn"), key.WithHelp("pgdn", "page down"))
	home := key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "oldest"))
	end := key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "follow live"))

	return KeyMap{
		Up: up, Down: down, Enter: enter, Escape: escape, Tab: tab, ShiftTab: shiftTab,
		Help: help, Quit: quit, ForceQuit: forceQuit,
		PageUp: pageUp, PageDown: pageDown, Home: home, End: end,
		Previous: up, Next: down, Open: enter, Back: escape, FocusNext: tab,
		FocusPrev: shiftTab, Interrupt: interrupt,
	}
}

// ShortHelp implements bubbles/help.KeyMap.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Tab, k.Help, k.Quit}
}

// ScrollBindings are the pane-navigation keys, in the order the footer and the
// expanded help both present them.
func (k KeyMap) ScrollBindings() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End}
}

// FullHelp implements bubbles/help.KeyMap.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Escape},
		{k.PageUp, k.PageDown, k.Home, k.End},
		{k.Tab, k.ShiftTab, k.Help, k.Interrupt, k.Quit},
	}
}
