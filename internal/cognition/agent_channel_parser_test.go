package cognition

import (
	"strings"
	"testing"
)

func TestParseAgentChannelParsesV1Kinds(t *testing.T) {
	tests := []struct {
		name string
		text string
		kind AgentChannelKind
	}{
		{
			name: "message",
			text: "```ghost-message\n{\"schema_version\":1,\"recipients\":[\"agent-2\"],\"thread_id\":\"thread-1\",\"type\":\"direct\",\"content\":\"hello\"}\n```",
			kind: AgentChannelMessage,
		},
		{
			name: "delegate",
			text: "```ghost-delegate\n{\"schema_version\":1,\"target\":\"agent-2\",\"parent_task_id\":\"task-1\",\"objective\":\"inspect parser\",\"context_refs\":[],\"expected_deliverable\":\"findings\",\"timeout\":\"30s\",\"ces_policy\":\"observe\",\"permissions\":{\"mode\":\"allow_list\",\"allow_tools\":[\"rg\"],\"deny_tools\":[]}}\n```",
			kind: AgentChannelDelegate,
		},
		{
			name: "consult",
			text: "```ghost-consult\n{\"schema_version\":1,\"target\":\"agent-2\",\"parent_task_id\":\"task-1\",\"question\":\"What is the boundary?\",\"evidence\":[\"line 4\"],\"references\":[]}\n```",
			kind: AgentChannelConsult,
		},
		{
			name: "result",
			text: "```ghost-result\n{\"schema_version\":1,\"task_id\":\"task-1\",\"status\":\"completed\",\"result\":\"Done\",\"uncertainty\":\"none\",\"references\":[]}\n```",
			kind: AgentChannelResult,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := ParseAgentChannel(tt.text)
			if err != nil || !found {
				t.Fatalf("found=%t err=%v", found, err)
			}
			if got.Kind != tt.kind {
				t.Fatalf("kind=%q, want %q", got.Kind, tt.kind)
			}
			count := 0
			for _, present := range []bool{got.Message != nil, got.Delegate != nil, got.Consult != nil, got.Result != nil} {
				if present {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("expected exactly one typed payload, got %#v", got)
			}
		})
	}
}

func TestParseAgentChannelParsesMemoryKinds(t *testing.T) {
	tests := []struct {
		text string
		kind AgentChannelKind
	}{
		{"```ghost-recall\n{\"query\":\"find the parser boundary\",\"scopes\":[\"workspace\"]}\n```", AgentChannelRecall},
		{"```ghost-remember\n{\"scope\":\"task\",\"claim\":\"observation\",\"text\":\"parser boundary\",\"evidence\":[\"event-1\"]}\n```", AgentChannelRemember},
		{"```ghost-challenge\n{\"record_id\":\"record-1\",\"text\":\"the claim is stale\",\"claim\":\"observation\",\"evidence\":[\"event-2\"]}\n```", AgentChannelChallenge},
	}
	for _, test := range tests {
		got, found, err := ParseAgentChannel(test.text)
		if err != nil || !found || got.Kind != test.kind {
			t.Fatalf("kind=%q found=%t err=%v; want %q", got.Kind, found, err, test.kind)
		}
	}
}

func TestParseAgentChannelRejectsInvalidMemoryBlocks(t *testing.T) {
	for _, text := range []string{
		"```ghost-recall\n{\"query\":\"x\",\"extra\":true}\n```",
		"```ghost-recall\n{\"query\":\"x\",\"scopes\":[\"private\"]}\n```",
		"```ghost-remember\n{\"scope\":\"global\",\"claim\":\"instruction\",\"text\":\"x\",\"unexpected\":1}\n```",
		"```ghost-challenge\n{\"record_id\":\"record-1\",\"text\":\"x\",\"claim\":\"invalid\"}\n```",
	} {
		got, found, err := ParseAgentChannel(text)
		if err == nil || !found || got.Kind != "" {
			t.Fatalf("got=%#v found=%t err=%v for %q", got, found, err, text)
		}
	}
}

func TestParseAgentChannelIgnoresProseAndUnrecognizedFences(t *testing.T) {
	for _, text := range []string{
		"hello @agent-2, can you inspect this?",
		"```text\n@agent-2\n```",
		"ordinary prose before and after",
	} {
		got, found, err := ParseAgentChannel(text)
		if err != nil || found || got.Kind != "" {
			t.Fatalf("text=%q got=%#v found=%t err=%v", text, got, found, err)
		}
	}
}

func TestParseAgentChannelRejectsMalformedAndInvalidPayloads(t *testing.T) {
	validPrefix := "```ghost-message\n"
	validSuffix := "\n```"
	tests := []struct {
		name string
		text string
	}{
		{name: "malformed json", text: validPrefix + `{` + validSuffix},
		{name: "unknown field", text: validPrefix + `{"schema_version":1,"recipients":["agent-2"],"thread_id":"thread-1","type":"direct","content":"hello","surprise":true}` + validSuffix},
		{name: "invalid schema version", text: validPrefix + `{"schema_version":2,"recipients":["agent-2"],"thread_id":"thread-1","type":"direct","content":"hello"}` + validSuffix},
		{name: "missing required field", text: validPrefix + `{"schema_version":1,"recipients":["agent-2"],"thread_id":"thread-1","type":"direct"}` + validSuffix},
		{name: "trailing json", text: validPrefix + `{"schema_version":1,"recipients":["agent-2"],"thread_id":"thread-1","type":"direct","content":"hello"}{}` + validSuffix},
		{name: "duplicate field", text: validPrefix + `{"schema_version":1,"recipients":["agent-2"],"thread_id":"thread-1","type":"direct","content":"hello","content":"again"}` + validSuffix},
		{name: "duplicate recipient", text: validPrefix + `{"schema_version":1,"recipients":["agent-2","agent-2"],"thread_id":"thread-1","type":"direct","content":"hello"}` + validSuffix},
		{name: "reply without message reference", text: validPrefix + `{"schema_version":1,"recipients":["agent-2"],"thread_id":"thread-1","type":"reply","content":"hello"}` + validSuffix},
		{name: "nested unknown field", text: "```ghost-delegate\n{\"schema_version\":1,\"target\":\"agent-2\",\"parent_task_id\":\"task-1\",\"objective\":\"inspect\",\"context_refs\":[],\"expected_deliverable\":\"findings\",\"permissions\":{\"mode\":\"read_only\",\"extra\":true}}\n```"},
		{name: "nested null field", text: "```ghost-delegate\n{\"schema_version\":1,\"target\":\"agent-2\",\"parent_task_id\":\"task-1\",\"objective\":\"inspect\",\"context_refs\":[],\"expected_deliverable\":\"findings\",\"permissions\":{\"mode\":\"read_only\",\"allow_tools\":null}}\n```"},
		{name: "unclosed fence", text: validPrefix + `{"schema_version":1,"recipients":["agent-2"],"thread_id":"thread-1","type":"direct","content":"hello"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := ParseAgentChannel(tt.text)
			if err == nil || !found || got.Kind != "" {
				t.Fatalf("got=%#v found=%t err=%v", got, found, err)
			}
		})
	}
}

func TestParseAgentChannelRejectsMultipleBlocks(t *testing.T) {
	message := "```ghost-message\n{\"schema_version\":1,\"recipients\":[\"agent-2\"],\"thread_id\":\"thread-1\",\"type\":\"direct\",\"content\":\"hello\"}\n```"
	consult := "```ghost-consult\n{\"schema_version\":1,\"target\":\"agent-2\",\"parent_task_id\":\"task-1\",\"question\":\"why?\"}\n```"
	got, found, err := ParseAgentChannel(message + "\n" + consult)
	if err == nil || !found || got.Kind != "" {
		t.Fatalf("got=%#v found=%t err=%v", got, found, err)
	}
}

func TestParseAgentChannelIgnoresQuotedAndNestedFences(t *testing.T) {
	message := "```ghost-message\n{\"schema_version\":1,\"recipients\":[\"agent-2\"],\"thread_id\":\"thread-1\",\"type\":\"direct\",\"content\":\"hello\"}\n```"
	for _, text := range []string{
		"> " + strings.ReplaceAll(message, "\n", "\n> "),
		"````markdown\n" + message + "\n````",
		"~~~text\n" + message + "\n~~~",
	} {
		got, found, err := ParseAgentChannel(text)
		if err != nil || found || got.Kind != "" {
			t.Fatalf("text=%q got=%#v found=%t err=%v", text, got, found, err)
		}
	}
}

func TestParseAgentChannelEnforcesContentLimit(t *testing.T) {
	text := "```ghost-message\n" + strings.Repeat("x", MaxAgentChannelBytes) + "\n```"
	got, found, err := ParseAgentChannel(text)
	if err == nil || found || got.Kind != "" {
		t.Fatalf("got=%#v found=%t err=%v", got, found, err)
	}
}

func TestParseAgentChannelValidatesChannelFields(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{
			name: "message type",
			text: "```ghost-message\n{\"schema_version\":1,\"recipients\":[\"agent-2\"],\"thread_id\":\"thread-1\",\"type\":\"broadcastish\",\"content\":\"hello\"}\n```",
		},
		{
			name: "delegate timeout",
			text: "```ghost-delegate\n{\"schema_version\":1,\"target\":\"agent-2\",\"parent_task_id\":\"task-1\",\"objective\":\"inspect\",\"context_refs\":[],\"expected_deliverable\":\"findings\",\"timeout\":\"soon\"}\n```",
		},
		{
			name: "permission mode",
			text: "```ghost-delegate\n{\"schema_version\":1,\"target\":\"agent-2\",\"parent_task_id\":\"task-1\",\"objective\":\"inspect\",\"context_refs\":[],\"expected_deliverable\":\"findings\",\"permissions\":{\"mode\":\"unrestricted\"}}\n```",
		},
		{
			name: "result status",
			text: "```ghost-result\n{\"schema_version\":1,\"task_id\":\"task-1\",\"status\":\"partial\",\"result\":\"Done\"}\n```",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := ParseAgentChannel(tt.text)
			if err == nil || !found || got.Kind != "" {
				t.Fatalf("got=%#v found=%t err=%v", got, found, err)
			}
		})
	}
}
