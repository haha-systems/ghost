package core

import (
	"fmt"
	"strings"
	"time"
)

type MessageType string

const (
	MessageDirect         MessageType = "direct"
	MessageBroadcast      MessageType = "broadcast"
	MessageRequest        MessageType = "request"
	MessageReply          MessageType = "reply"
	MessageTaskDelegation MessageType = "task_delegation"
	MessageTaskResult     MessageType = "task_result"
	MessageSystem         MessageType = "system"
)

type DeliveryStatus string

const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryRead      DeliveryStatus = "read"
	DeliveryFailed    DeliveryStatus = "failed"
)

const BroadcastWildcard AgentID = "*"

// Message is a runtime communication record. Delivery is transport state,
// while the message identity and content remain stable.
type Message struct {
	ID         MessageID      `json:"id"`
	Sender     AgentID        `json:"sender,omitempty"`
	Recipients []AgentID      `json:"recipients,omitempty"`
	ThreadID   ThreadID       `json:"thread_id"`
	TaskID     *TaskID        `json:"task_id,omitempty"`
	ReplyTo    *MessageID     `json:"reply_to,omitempty"`
	Type       MessageType    `json:"type"`
	Content    string         `json:"content"`
	CreatedAt  time.Time      `json:"created_at"`
	Delivery   DeliveryStatus `json:"delivery"`
}

func (m Message) Validate() error {
	if err := m.ID.Validate(); err != nil {
		return fmt.Errorf("message ID: %w", err)
	}
	if m.Type != MessageSystem || m.Sender != "" {
		if err := m.Sender.Validate(); err != nil {
			return fmt.Errorf("sender: %w", err)
		}
	}
	if err := m.ThreadID.Validate(); err != nil {
		return fmt.Errorf("thread ID: %w", err)
	}
	if m.TaskID != nil {
		if err := m.TaskID.Validate(); err != nil {
			return fmt.Errorf("task ID: %w", err)
		}
	}
	if m.ReplyTo != nil {
		if err := m.ReplyTo.Validate(); err != nil {
			return fmt.Errorf("reply_to: %w", err)
		}
	}
	if strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("message content is required")
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("message created_at is required")
	}
	switch m.Delivery {
	case DeliveryPending, DeliveryDelivered, DeliveryRead, DeliveryFailed:
	default:
		return fmt.Errorf("unknown delivery status %q", m.Delivery)
	}
	seenRecipients := make(map[AgentID]struct{}, len(m.Recipients))
	for _, recipient := range m.Recipients {
		if recipient == BroadcastWildcard && m.Type == MessageBroadcast {
			continue
		}
		if err := recipient.Validate(); err != nil {
			return fmt.Errorf("recipient: %w", err)
		}
		if _, exists := seenRecipients[recipient]; exists {
			return fmt.Errorf("duplicate recipient %q", recipient)
		}
		seenRecipients[recipient] = struct{}{}
	}
	switch m.Type {
	case MessageDirect, MessageRequest:
		if len(m.Recipients) == 0 {
			return fmt.Errorf("%s message requires recipients", m.Type)
		}
	case MessageBroadcast:
		if len(m.Recipients) != 0 && !(len(m.Recipients) == 1 && m.Recipients[0] == BroadcastWildcard) {
			return fmt.Errorf("broadcast must have no explicit recipients or one wildcard")
		}
	case MessageReply:
		if m.ReplyTo == nil || len(m.Recipients) == 0 {
			return fmt.Errorf("reply requires reply_to and recipients")
		}
	case MessageTaskDelegation, MessageTaskResult:
		if m.TaskID == nil || len(m.Recipients) == 0 {
			return fmt.Errorf("%s message requires task_id and recipients", m.Type)
		}
	case MessageSystem:
	default:
		return fmt.Errorf("unknown message type %q", m.Type)
	}
	return nil
}
