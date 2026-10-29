package core

import (
	"fmt"
	"strings"
)

type RuntimeKind string
type ProviderKind string
type Capability string

const (
	CapabilityCodeExecution   Capability = "code_execution"
	CapabilityReview          Capability = "review"
	CapabilityConsultation    Capability = "consultation"
	CapabilityClassification  Capability = "classification"
	CapabilityDecisionSupport Capability = "decision_support"
	CapabilityMemoryAccess    Capability = "memory_access"
	CapabilityDelegation      Capability = "delegation"
)

func (c Capability) Validate() error {
	switch c {
	case CapabilityCodeExecution, CapabilityReview, CapabilityConsultation, CapabilityClassification,
		CapabilityDecisionSupport, CapabilityMemoryAccess, CapabilityDelegation:
		return nil
	default:
		return fmt.Errorf("unknown capability %q", c)
	}
}

const (
	RuntimeCodex            RuntimeKind = "codex"
	RuntimeClaude           RuntimeKind = "claude"
	RuntimeOpenAICompatible RuntimeKind = "openai-compat"
)

type Availability string

const (
	AvailabilityAvailable Availability = "available"
	AvailabilityBusy      Availability = "busy"
	AvailabilityOffline   Availability = "offline"
	AvailabilityError     Availability = "error"
)

type PermissionMode string

const (
	PermissionModeInherit   PermissionMode = "inherit"
	PermissionModeAllowList PermissionMode = "allow_list"
	PermissionModeDenyList  PermissionMode = "deny_list"
	PermissionModeAsk       PermissionMode = "ask"
)

type PermissionPolicy struct {
	Mode       PermissionMode `json:"mode"`
	AllowTools []string       `json:"allow_tools,omitempty"`
	DenyTools  []string       `json:"deny_tools,omitempty"`
}

type SessionBinding struct {
	SessionID          SessionID `json:"session_id"`
	ProviderSessionRef string    `json:"provider_session_ref,omitempty"`
}

// Agent is the runtime-owned identity and current assignment contract.
type Agent struct {
	ID               AgentID          `json:"id"`
	DisplayName      string           `json:"display_name"`
	Runtime          RuntimeKind      `json:"runtime"`
	Provider         ProviderKind     `json:"provider,omitempty"`
	SessionBinding   *SessionBinding  `json:"session_binding,omitempty"`
	Availability     Availability     `json:"availability"`
	Activity         string           `json:"activity,omitempty"`
	Capabilities     []Capability     `json:"capabilities,omitempty"`
	PermissionPolicy PermissionPolicy `json:"permission_policy"`
	AssignedTasks    []TaskID         `json:"assigned_tasks,omitempty"`
	ActiveTask       []TaskID         `json:"active_tasks,omitempty"`
}

func (a Agent) Validate() error {
	if err := a.ID.Validate(); err != nil {
		return fmt.Errorf("agent ID: %w", err)
	}
	if strings.TrimSpace(a.DisplayName) == "" {
		return fmt.Errorf("agent display name is required")
	}
	if strings.TrimSpace(string(a.Runtime)) == "" {
		return fmt.Errorf("agent runtime is required")
	}
	switch a.Availability {
	case AvailabilityAvailable, AvailabilityBusy, AvailabilityOffline, AvailabilityError:
	default:
		return fmt.Errorf("unknown availability %q", a.Availability)
	}
	if a.SessionBinding != nil {
		if err := a.SessionBinding.SessionID.Validate(); err != nil {
			return fmt.Errorf("session binding: %w", err)
		}
	}
	if err := validateUniqueStrings("capability", a.Capabilities); err != nil {
		return err
	}
	for _, capability := range a.Capabilities {
		if err := capability.Validate(); err != nil {
			return fmt.Errorf("agent capability: %w", err)
		}
	}
	if err := a.PermissionPolicy.Validate(); err != nil {
		return fmt.Errorf("permission policy: %w", err)
	}
	if err := validateTaskIDs(a.AssignedTasks); err != nil {
		return fmt.Errorf("assigned task: %w", err)
	}
	if err := validateTaskIDs(a.ActiveTask); err != nil {
		return fmt.Errorf("active task: %w", err)
	}
	return nil
}

func (p PermissionPolicy) Validate() error {
	switch p.Mode {
	case PermissionModeInherit, PermissionModeAllowList, PermissionModeDenyList, PermissionModeAsk:
	default:
		return fmt.Errorf("unknown permission mode %q", p.Mode)
	}
	if err := validateUniqueStrings("allowed tool", p.AllowTools); err != nil {
		return err
	}
	if err := validateUniqueStrings("denied tool", p.DenyTools); err != nil {
		return err
	}
	denied := make(map[string]struct{}, len(p.DenyTools))
	for _, tool := range p.DenyTools {
		denied[tool] = struct{}{}
	}
	for _, tool := range p.AllowTools {
		if _, exists := denied[tool]; exists {
			return fmt.Errorf("tool %q is both allowed and denied", tool)
		}
	}
	return nil
}

func validateUniqueStrings[T ~string](label string, values []T) error {
	seen := make(map[string]struct{}, len(values))
	for _, item := range values {
		value := strings.TrimSpace(string(item))
		if value == "" {
			return fmt.Errorf("%s is empty", label)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateAgentIDs(ids []AgentID) error {
	seen := map[AgentID]struct{}{}
	for _, id := range ids {
		if err := id.Validate(); err != nil {
			return err
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate ID %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}
