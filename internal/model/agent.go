// Package model contains the small presentation models used by the UI.
package model

type AgentState string

const (
	AgentIdle    AgentState = "idle"
	AgentActive  AgentState = "active"
	AgentWaiting AgentState = "waiting"
	AgentDone    AgentState = "done"
	AgentError   AgentState = "error"
)

type Agent struct {
	ID       string
	Callsign string
	Client   string
	Runtime  string
	Model    string
	State    AgentState
	Activity string
}

func MockAgents() []Agent {
	return []Agent{
		{ID: "veil", Callsign: "VEIL", Client: "CODEX", Runtime: "18m 42s", Model: "placeholder", State: AgentActive, Activity: "indexing repo"},
		{ID: "wraith", Callsign: "WRAITH", Client: "CLAUDE", Runtime: "12m 08s", Model: "placeholder", State: AgentActive, Activity: "running tests"},
		{ID: "shade", Callsign: "SHADE", Client: "DEEPSEEK", Runtime: "—", Model: "placeholder", State: AgentIdle, Activity: "awaiting task"},
	}
}
