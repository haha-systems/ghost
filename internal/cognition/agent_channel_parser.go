package cognition

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// MaxAgentChannelBytes caps the full response and each channel JSON body at
// 16 KiB. This bounds both scanning and decoding work.
const MaxAgentChannelBytes = 16 << 10

type AgentChannelKind string

const (
	AgentChannelMessage  AgentChannelKind = "ghost-message"
	AgentChannelDelegate AgentChannelKind = "ghost-delegate"
	AgentChannelConsult  AgentChannelKind = "ghost-consult"
	AgentChannelResult   AgentChannelKind = "ghost-result"
)

// AgentChannelRequest contains exactly one channel-specific v1 payload.
type AgentChannelRequest struct {
	Kind     AgentChannelKind
	Message  *GhostMessageRequest
	Delegate *GhostDelegateRequest
	Consult  *GhostConsultRequest
	Result   *GhostResultRequest
}

type GhostMessageRequest struct {
	SchemaVersion int      `json:"schema_version"`
	Recipients    []string `json:"recipients"`
	ThreadID      string   `json:"thread_id"`
	TaskID        string   `json:"task_id,omitempty"`
	Type          string   `json:"type"`
	Content       string   `json:"content"`
	ReplyTo       string   `json:"reply_to,omitempty"`
}

type GhostDelegateRequest struct {
	SchemaVersion       int                      `json:"schema_version"`
	Target              string                   `json:"target"`
	ParentTaskID        string                   `json:"parent_task_id"`
	Objective           string                   `json:"objective"`
	ContextRefs         []string                 `json:"context_refs"`
	ExpectedDeliverable string                   `json:"expected_deliverable"`
	Timeout             string                   `json:"timeout,omitempty"`
	CESPolicy           string                   `json:"ces_policy,omitempty"`
	Permissions         *GhostChannelPermissions `json:"permissions,omitempty"`
}

type GhostChannelPermissions struct {
	Mode       string   `json:"mode"`
	AllowTools []string `json:"allow_tools,omitempty"`
	DenyTools  []string `json:"deny_tools,omitempty"`
}

type GhostConsultRequest struct {
	SchemaVersion int      `json:"schema_version"`
	Target        string   `json:"target"`
	ParentTaskID  string   `json:"parent_task_id"`
	Question      string   `json:"question"`
	Evidence      []string `json:"evidence,omitempty"`
	References    []string `json:"references,omitempty"`
}

type GhostResultRequest struct {
	SchemaVersion int      `json:"schema_version"`
	TaskID        string   `json:"task_id"`
	Status        string   `json:"status"`
	Result        string   `json:"result"`
	Uncertainty   string   `json:"uncertainty,omitempty"`
	References    []string `json:"references,omitempty"`
}

// ParseAgentChannel returns the single valid channel request in text. Ordinary
// prose, quoted blocks, and blocks inside other Markdown fences are ignored.
// found reports whether a recognized channel fence was present, including
// when its payload is invalid.
func ParseAgentChannel(text string) (request AgentChannelRequest, found bool, err error) {
	if len(text) > MaxAgentChannelBytes {
		return AgentChannelRequest{}, false, fmt.Errorf("agent channel: response exceeds %d bytes", MaxAgentChannelBytes)
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var requests []AgentChannelRequest
	var fence byte
	var fenceLength int

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if isMarkdownQuote(line) {
			continue
		}
		if fence != 0 {
			if isFenceClose(line, fence, fenceLength) {
				fence, fenceLength = 0, 0
			}
			continue
		}

		if kind, ok := agentChannelFence(line); ok {
			var body []string
			bodySize := 0
			closed := false
			for i++; i < len(lines); i++ {
				if isMarkdownQuote(lines[i]) {
					body = append(body, lines[i])
				} else if isFenceClose(lines[i], '`', 3) {
					closed = true
					break
				} else {
					body = append(body, lines[i])
				}
				bodySize += len(lines[i])
				if len(body) > 1 {
					bodySize++
				}
				if bodySize > MaxAgentChannelBytes {
					return AgentChannelRequest{}, true, fmt.Errorf("agent channel %s: content exceeds %d bytes", kind, MaxAgentChannelBytes)
				}
			}
			if !closed {
				return AgentChannelRequest{}, true, fmt.Errorf("agent channel %s: missing closing fence", kind)
			}
			parsed, parseErr := parseAgentChannelPayload(kind, []byte(strings.Join(body, "\n")))
			if parseErr != nil {
				return AgentChannelRequest{}, true, parseErr
			}
			requests = append(requests, parsed)
			continue
		}

		if marker, length, ok := markdownFenceOpen(line); ok {
			fence, fenceLength = marker, length
		}
	}

	if len(requests) > 1 {
		return AgentChannelRequest{}, true, errors.New("agent channel: expected one channel block, found multiple")
	}
	if len(requests) == 0 {
		return AgentChannelRequest{}, false, nil
	}
	return requests[0], true, nil
}

func parseAgentChannelPayload(kind AgentChannelKind, data []byte) (AgentChannelRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return AgentChannelRequest{}, channelError(kind, "invalid JSON", err)
	}
	if fields == nil {
		return AgentChannelRequest{}, channelError(kind, "payload must be a JSON object", nil)
	}
	if err := rejectDuplicateJSONFields(data); err != nil {
		return AgentChannelRequest{}, channelError(kind, "invalid JSON", err)
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return AgentChannelRequest{}, fmt.Errorf("agent channel %s: field %q must not be null", kind, name)
		}
	}

	request := AgentChannelRequest{Kind: kind}
	switch kind {
	case AgentChannelMessage:
		var value GhostMessageRequest
		if err := decodeAgentChannel(kind, data, &value, "schema_version", "recipients", "thread_id", "type", "content"); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireNonEmpty(kind, "thread_id", value.ThreadID); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireNonEmpty(kind, "content", value.Content); err != nil {
			return AgentChannelRequest{}, err
		}
		if len(value.Recipients) == 0 {
			return AgentChannelRequest{}, fmt.Errorf("agent channel %s: recipients must contain at least one recipient", kind)
		}
		if err := requireItems(kind, "recipients", value.Recipients); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireUniqueItems(kind, "recipients", value.Recipients); err != nil {
			return AgentChannelRequest{}, err
		}
		if value.Type != "direct" && value.Type != "broadcast" && value.Type != "request" && value.Type != "reply" {
			return AgentChannelRequest{}, fmt.Errorf("agent channel %s: invalid type %q", kind, value.Type)
		}
		if value.Type == "reply" && strings.TrimSpace(value.ReplyTo) == "" {
			return AgentChannelRequest{}, fmt.Errorf("agent channel %s: reply_to is required for a reply", kind)
		}
		if value.Type != "reply" && value.ReplyTo != "" {
			return AgentChannelRequest{}, fmt.Errorf("agent channel %s: reply_to is only valid for a reply", kind)
		}
		if value.Type != "broadcast" && hasString(value.Recipients, "*") {
			return AgentChannelRequest{}, fmt.Errorf("agent channel %s: wildcard recipient requires broadcast type", kind)
		}
		request.Message = &value
	case AgentChannelDelegate:
		var value GhostDelegateRequest
		if err := decodeAgentChannel(kind, data, &value, "schema_version", "target", "parent_task_id", "objective", "context_refs", "expected_deliverable"); err != nil {
			return AgentChannelRequest{}, err
		}
		if raw, ok := fields["permissions"]; ok {
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(raw, &nested); err != nil || nested == nil {
				return AgentChannelRequest{}, fmt.Errorf("agent channel %s: permissions must be an object", kind)
			}
			if err := validateObjectFields(kind, "permissions", nested, []string{"mode"}, []string{"mode", "allow_tools", "deny_tools"}); err != nil {
				return AgentChannelRequest{}, err
			}
		}
		if err := requireNonEmptyFields(kind,
			"target", value.Target,
			"parent_task_id", value.ParentTaskID,
			"objective", value.Objective,
			"expected_deliverable", value.ExpectedDeliverable,
		); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireItems(kind, "context_refs", value.ContextRefs); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireUniqueItems(kind, "context_refs", value.ContextRefs); err != nil {
			return AgentChannelRequest{}, err
		}
		if value.Timeout != "" {
			duration, err := time.ParseDuration(value.Timeout)
			if err != nil || duration <= 0 {
				return AgentChannelRequest{}, fmt.Errorf("agent channel %s: timeout must be a positive duration", kind)
			}
		}
		if value.CESPolicy != "" && value.CESPolicy != "off" && value.CESPolicy != "observe" && value.CESPolicy != "enforce" {
			return AgentChannelRequest{}, fmt.Errorf("agent channel %s: invalid ces_policy %q", kind, value.CESPolicy)
		}
		if value.Permissions != nil {
			if err := validateChannelPermissions(kind, *value.Permissions); err != nil {
				return AgentChannelRequest{}, err
			}
		}
		request.Delegate = &value
	case AgentChannelConsult:
		var value GhostConsultRequest
		if err := decodeAgentChannel(kind, data, &value, "schema_version", "target", "parent_task_id", "question"); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireNonEmptyFields(kind,
			"target", value.Target,
			"parent_task_id", value.ParentTaskID,
			"question", value.Question,
		); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireItems(kind, "evidence", value.Evidence); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireItems(kind, "references", value.References); err != nil {
			return AgentChannelRequest{}, err
		}
		request.Consult = &value
	case AgentChannelResult:
		var value GhostResultRequest
		if err := decodeAgentChannel(kind, data, &value, "schema_version", "task_id", "status", "result"); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireNonEmpty(kind, "task_id", value.TaskID); err != nil {
			return AgentChannelRequest{}, err
		}
		if value.Status != "completed" && value.Status != "failed" {
			return AgentChannelRequest{}, fmt.Errorf("agent channel %s: invalid status %q", kind, value.Status)
		}
		if err := requireNonEmpty(kind, "result", value.Result); err != nil {
			return AgentChannelRequest{}, err
		}
		if err := requireItems(kind, "references", value.References); err != nil {
			return AgentChannelRequest{}, err
		}
		request.Result = &value
	default:
		return AgentChannelRequest{}, fmt.Errorf("agent channel: unsupported channel %q", kind)
	}
	return request, nil
}

func decodeAgentChannel(kind AgentChannelKind, data []byte, destination any, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return channelError(kind, "invalid JSON", err)
	}
	if err := validateObjectFields(kind, "payload", fields, required, agentChannelFields(kind)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return channelError(kind, "invalid payload", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("agent channel %s: trailing JSON value", kind)
		}
		return channelError(kind, "invalid trailing data", err)
	}
	var version struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return channelError(kind, "invalid schema_version", err)
	}
	if version.SchemaVersion != 1 {
		return fmt.Errorf("agent channel %s: unsupported schema_version %d", kind, version.SchemaVersion)
	}
	return nil
}

func validateChannelPermissions(kind AgentChannelKind, value GhostChannelPermissions) error {
	if err := requireNonEmpty(kind, "permissions.mode", value.Mode); err != nil {
		return err
	}
	if value.Mode != "inherit" && value.Mode != "allow_list" && value.Mode != "deny_list" && value.Mode != "ask" {
		return fmt.Errorf("agent channel %s: invalid permissions.mode %q", kind, value.Mode)
	}
	if err := requireItems(kind, "permissions.allow_tools", value.AllowTools); err != nil {
		return err
	}
	if err := requireItems(kind, "permissions.deny_tools", value.DenyTools); err != nil {
		return err
	}
	if err := requireUniqueItems(kind, "permissions.allow_tools", value.AllowTools); err != nil {
		return err
	}
	if err := requireUniqueItems(kind, "permissions.deny_tools", value.DenyTools); err != nil {
		return err
	}
	for _, allowed := range value.AllowTools {
		if hasString(value.DenyTools, allowed) {
			return fmt.Errorf("agent channel %s: tool %q is both allowed and denied", kind, allowed)
		}
	}
	return nil
}

func requireNonEmpty(kind AgentChannelKind, field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("agent channel %s: %s must not be empty", kind, field)
	}
	return nil
}

func requireNonEmptyFields(kind AgentChannelKind, nameAndValues ...string) error {
	for i := 0; i+1 < len(nameAndValues); i += 2 {
		if err := requireNonEmpty(kind, nameAndValues[i], nameAndValues[i+1]); err != nil {
			return err
		}
	}
	return nil
}

func requireItems(kind AgentChannelKind, field string, values []string) error {
	for i, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("agent channel %s: %s[%d] must not be empty", kind, field, i)
		}
	}
	return nil
}

func requireUniqueItems(kind AgentChannelKind, field string, values []string) error {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return fmt.Errorf("agent channel %s: duplicate %s value %q", kind, field, value)
		}
		seen[value] = true
	}
	return nil
}

func validateObjectFields(kind AgentChannelKind, objectName string, fields map[string]json.RawMessage, required, allowed []string) error {
	if fields == nil {
		return fmt.Errorf("agent channel %s: %s must be an object", kind, objectName)
	}
	for name := range fields {
		if !hasString(allowed, name) {
			return fmt.Errorf("agent channel %s: unknown field %q in %s", kind, name, objectName)
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("agent channel %s: missing required field %q in %s", kind, name, objectName)
		}
	}
	return nil
}

func hasString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func agentChannelFields(kind AgentChannelKind) []string {
	switch kind {
	case AgentChannelMessage:
		return []string{"schema_version", "recipients", "thread_id", "task_id", "type", "content", "reply_to"}
	case AgentChannelDelegate:
		return []string{"schema_version", "target", "parent_task_id", "objective", "context_refs", "expected_deliverable", "timeout", "ces_policy", "permissions"}
	case AgentChannelConsult:
		return []string{"schema_version", "target", "parent_task_id", "question", "evidence", "references"}
	case AgentChannelResult:
		return []string{"schema_version", "task_id", "status", "result", "uncertainty", "references"}
	default:
		return nil
	}
}

func channelError(kind AgentChannelKind, description string, err error) error {
	if err == nil {
		return fmt.Errorf("agent channel %s: %s", kind, description)
	}
	return fmt.Errorf("agent channel %s: %s: %w", kind, description, err)
}

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := readJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func readJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("null values are not allowed")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate field %q", key)
			}
			seen[key] = struct{}{}
			if err := readJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := readJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func agentChannelFence(line string) (AgentChannelKind, bool) {
	indent := leadingSpaces(line)
	if indent > 3 {
		return "", false
	}
	rest := strings.TrimRight(line[indent:], " \t")
	switch rest {
	case "```ghost-message":
		return AgentChannelMessage, true
	case "```ghost-delegate":
		return AgentChannelDelegate, true
	case "```ghost-consult":
		return AgentChannelConsult, true
	case "```ghost-result":
		return AgentChannelResult, true
	default:
		return "", false
	}
}

func markdownFenceOpen(line string) (byte, int, bool) {
	indent := leadingSpaces(line)
	if indent > 3 {
		return 0, 0, false
	}
	rest := line[indent:]
	if len(rest) == 0 || (rest[0] != '`' && rest[0] != '~') {
		return 0, 0, false
	}
	marker := rest[0]
	length := 0
	for length < len(rest) && rest[length] == marker {
		length++
	}
	if length < 3 || (marker == '`' && strings.Contains(rest[length:], "`")) {
		return 0, 0, false
	}
	return marker, length, true
}

func isFenceClose(line string, marker byte, minimum int) bool {
	indent := leadingSpaces(line)
	if indent > 3 {
		return false
	}
	rest := line[indent:]
	length := 0
	for length < len(rest) && rest[length] == marker {
		length++
	}
	return length >= minimum && strings.TrimSpace(rest[length:]) == ""
}

func leadingSpaces(line string) int {
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' {
			return i
		}
	}
	return len(line)
}

func isMarkdownQuote(line string) bool {
	indent := leadingSpaces(line)
	return indent <= 3 && strings.HasPrefix(line[indent:], ">")
}
