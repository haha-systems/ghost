package ghostdive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/memory"
)

const (
	spaceID = "123e4567-e89b-12d3-a456-426614174000"
	wraith  = core.AgentID("agent_00000000000000010000000000000001")
	veil    = core.AgentID("agent_00000000000000010000000000000002")
)

func TestObserveWireRequestAndProvenanceEnvelope(t *testing.T) {
	var got observeRequest
	var rawBody map[string]any
	var method, path, authorization, userAgent string
	server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		authorization, userAgent = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if err := json.Unmarshal(data, &rawBody); err != nil {
			t.Errorf("decode raw request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"saved-1","summary":"saved","promotion_state":"observed","confidence":0.9}`)
	}))
	defer server.Close()
	client := mustClient(t, Options{BaseURL: server.URL, BearerKey: "secret-key", HTTPClient: httpClient})
	provenance := core.MemoryProvenance{
		ClaimKind: core.ClaimVerifiedResult, Agent: wraith, Task: "task_00000000000000010000000000000001",
		Workspace: "project", EventRefs: []string{"event-1"}, ArtifactRefs: []string{"artifact-1"},
		Evidence: []string{"evidence-1"}, Counterevidence: []string{"counter-1"},
		Confidence: 1, Validation: core.ValidationVerified,
		RecordedAt: time.Date(2026, 10, 25, 10, 30, 0, 0, time.UTC), Challenges: "challenged-1",
	}
	text := strings.Repeat("界", 4005)
	stored, err := client.Observe(context.Background(), core.MemoryBackendWrite{
		Space:  core.MemorySpace{ID: spaceID, Scope: core.MemoryScopeWorkspace},
		Record: core.MemoryRecord{Scope: core.MemoryScopeWorkspace, Text: text, Subject: strings.Repeat("s", 250), Provenance: provenance},
	})
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/v1/spaces/"+spaceID+"/memories" || authorization != "Bearer secret-key" || userAgent != "ghost" {
		t.Fatalf("request = %s %s auth=%q agent=%q", method, path, authorization, userAgent)
	}
	if got.Layer != "episodic" || got.Summary != strings.Repeat("界", 4000) || got.Subject != strings.Repeat("s", 200) || got.AgentID != string(wraith) || got.Confidence != 0.9 || !got.SkipAutoSupersede {
		t.Fatalf("observe body = %#v", got)
	}
	wantBody := map[string]any{
		"layer": "episodic", "subject": strings.Repeat("s", 200), "summary": strings.Repeat("界", 4000),
		"confidence": 0.9, "agent_id": string(wraith), "skip_auto_supersede": true,
		"provenance": map[string]any{
			"source_kind": "ghost/verified_result", "source_ref": "ghost://project/workspace/" + string(wraith),
			"source_record_id": "challenged-1", "note": got.Provenance.Note,
		},
		"evidence_refs": []any{"event-1", "artifact-1", "evidence-1"},
	}
	if !reflect.DeepEqual(rawBody, wantBody) {
		t.Fatalf("observe JSON = %#v, want %#v", rawBody, wantBody)
	}
	if stored.ID != "saved-1" {
		t.Fatalf("stored record = %#v", stored)
	}
	if got.Provenance.SourceKind != "ghost/verified_result" || got.Provenance.SourceRef != "ghost://project/workspace/"+string(wraith) || got.Provenance.SourceRecordID != "challenged-1" {
		t.Fatalf("wire provenance = %#v", got.Provenance)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(got.Provenance.Note), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["v"] != float64(1) || envelope["scope"] != "workspace" || envelope["agent"] != string(wraith) || envelope["task"] != string(provenance.Task) || envelope["workspace"] != "project" || envelope["claim"] != "verified_result" || envelope["validation"] != "verified" || envelope["recorded_at"] != provenance.RecordedAt.Format(time.RFC3339Nano) {
		t.Fatalf("envelope = %#v", envelope)
	}
	for key, want := range map[string][]string{
		"evidence": {"evidence-1"}, "counterevidence": {"counter-1"},
		"events": {"event-1"}, "artifacts": {"artifact-1"},
	} {
		if !reflectStrings(envelope[key], want) {
			t.Errorf("envelope[%q] = %#v, want %v", key, envelope[key], want)
		}
	}
	if !reflectStrings(got.EvidenceRefs, []string{"event-1", "artifact-1", "evidence-1"}) {
		t.Fatalf("evidence_refs = %v", got.EvidenceRefs)
	}
}

func TestRetrieveWireRequestDecodesHitsAndUnknownFields(t *testing.T) {
	var body map[string]any
	var method, path, authorization string
	note := envelopeJSON(t, envelope{Version: 1, Scope: core.MemoryScopeWorkspace, Agent: wraith, Task: "task-1", Workspace: "project", Claim: core.ClaimVerifiedResult, Validation: core.ValidationVerified, Evidence: []string{"event-1"}, RecordedAt: time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)})
	server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		authorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"future_response_field":true,"hits":[{"id":"hit-1","summary":"saved result","confidence":0.85,"promotion_state":"accepted","agent_id":%q,"provenance":{"source_kind":"ghost/verified_result","source_ref":"ghost://project/workspace/%s","source_record_id":"","note":%q},"evidence_refs":["event-1"],"score":0.83,"future_hit_field":"ignored"}]}`, string(wraith), string(wraith), note)
	}))
	defer server.Close()
	client := mustClient(t, Options{BaseURL: server.URL, BearerKey: "retrieve-key", HTTPClient: httpClient})
	items, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID, Scope: core.MemoryScopeWorkspace}, Query: "saved result", TopK: 4})
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/v1/spaces/"+spaceID+"/memories/retrieve" || authorization != "Bearer retrieve-key" {
		t.Fatalf("request = %s %s auth=%q", method, path, authorization)
	}
	if !reflectJSON(body, map[string]any{"query": "saved result", "top_k": float64(4), "mode": "hybrid"}) {
		t.Fatalf("retrieve body = %#v", body)
	}
	if len(items) != 1 || items[0].ID != "hit-1" || items[0].Score != 0.83 || items[0].BackendState != "accepted" || items[0].Foreign || items[0].Provenance.Agent != wraith || items[0].Provenance.Validation != core.ValidationVerified || items[0].Provenance.MissingEvidence {
		t.Fatalf("retrieved records = %#v", items)
	}
}

func TestForeignRecordKeepsReportedConfidenceAndBackendState(t *testing.T) {
	server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"hits":[{"id":"foreign-1","summary":"outside record","confidence":0.97,"promotion_state":"accepted","provenance":{"source_kind":"someone-else","note":"not an envelope"},"evidence_refs":[],"score":0.6}]}`)
	}))
	defer server.Close()
	client := mustClient(t, Options{BaseURL: server.URL, HTTPClient: httpClient})
	items, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID, Scope: core.MemoryScopeWorkspace}, Query: "outside", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].Foreign || items[0].Scope != core.MemoryScopeWorkspace || items[0].ReportedConfidence != 0.97 || items[0].BackendState != "accepted" || items[0].Provenance.Validation != core.ValidationUnverified || !items[0].Provenance.MissingEvidence {
		t.Fatalf("foreign record = %#v", items)
	}
}

func TestGetByIDUsesDocumentedRoute(t *testing.T) {
	var method, path string
	server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_, _ = io.WriteString(w, `{"id":"record-7","summary":"by id","confidence":0.4,"promotion_state":"candidate"}`)
	}))
	defer server.Close()
	client := mustClient(t, Options{BaseURL: server.URL, HTTPClient: httpClient})
	item, err := client.Get(context.Background(), core.MemorySpace{ID: spaceID, Scope: core.MemoryScopeWorkspace}, "record-7")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet || path != "/v1/spaces/"+spaceID+"/memories/record-7" || item.ID != "record-7" || item.Text != "by id" {
		t.Fatalf("get = %#v, request=%s %s", item, method, path)
	}
}

func TestRedirectDoesNotCallAnUnlistedEndpoint(t *testing.T) {
	calls := 0
	server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "/v1/spaces/"+spaceID+"/memories/record-1/transitions")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client := mustClient(t, Options{BaseURL: server.URL, HTTPClient: httpClient})
	_, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID}, Query: "x"})
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) || calls != 1 {
		t.Fatalf("redirect response = %v, server calls = %d", err, calls)
	}
}

func TestDecodeRevalidatesVerifiedEnvelopeClaims(t *testing.T) {
	base := envelope{Version: 1, Scope: core.MemoryScopeWorkspace, Agent: wraith, Task: "task-1", Workspace: "project", Validation: core.ValidationVerified, Evidence: []string{"event-1"}, RecordedAt: time.Now().UTC()}
	base.Claim = core.ClaimInference
	item := decodeRecord(recordDTO{ID: "claim-1", Confidence: 0.95, Provenance: provenanceDTO{
		SourceKind: "ghost/inference", SourceRef: sourceRef(core.MemoryProvenance{Agent: wraith, Workspace: "project"}, core.MemoryScopeWorkspace), Note: envelopeJSON(t, base),
	}}, core.MemorySpace{ID: spaceID, Scope: core.MemoryScopeWorkspace}, 0.5)
	if item.Foreign || item.Provenance.Validation != core.ValidationUnverified || item.Provenance.Confidence > 0.5 || item.Provenance.MissingEvidence {
		t.Fatalf("unverified inference = %#v", item)
	}

	base.Claim = core.ClaimVerifiedResult
	base.Evidence = nil
	item = decodeRecord(recordDTO{ID: "claim-2", Confidence: 0.9, Provenance: provenanceDTO{
		SourceKind: "ghost/verified_result", SourceRef: sourceRef(core.MemoryProvenance{Agent: wraith, Workspace: "project"}, core.MemoryScopeWorkspace), Note: envelopeJSON(t, base),
	}}, core.MemorySpace{ID: spaceID, Scope: core.MemoryScopeWorkspace}, 0.4)
	if item.Foreign || item.Provenance.Validation != core.ValidationUnverified || !item.Provenance.MissingEvidence {
		t.Fatalf("verified result without evidence = %#v", item)
	}
}

func TestHTTPErrorMappingAndKeyRedaction(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"error":"failed","code":"code-%d","request_id":"request-%d"}`, status, status)
			}))
			defer server.Close()
			client := mustClient(t, Options{BaseURL: server.URL, BearerKey: "private-key", HTTPClient: httpClient})
			_, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID}, Query: "x"})
			if err == nil {
				t.Fatal("Retrieve succeeded for an error response")
			}
			if status == http.StatusTooManyRequests {
				var quota *QuotaExceededError
				if !errors.As(err, &quota) || quota.Code != "code-429" || quota.RequestID != "request-429" {
					t.Fatalf("error = %#v, want quota error", err)
				}
			} else if status >= 500 {
				var unavailable *UnavailableError
				if !errors.As(err, &unavailable) || unavailable.StatusCode != status || unavailable.Code != "code-500" || unavailable.RequestID != "request-500" {
					t.Fatalf("error = %#v, want unavailable", err)
				}
			} else {
				var rejected *RejectedError
				if !errors.As(err, &rejected) || rejected.StatusCode != status || rejected.Code != fmt.Sprintf("code-%d", status) || rejected.RequestID != fmt.Sprintf("request-%d", status) {
					t.Fatalf("error = %#v, want rejected", err)
				}
			}
		})
	}

	server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"private-key","code":"private-key","request_id":"private-key"}`)
	}))
	defer server.Close()
	client := mustClient(t, Options{BaseURL: server.URL, BearerKey: "private-key", HTTPClient: httpClient})
	_, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID}, Query: "x"})
	if err == nil || strings.Contains(err.Error(), "private-key") || strings.Contains(fmt.Sprint(client), "private-key") || strings.Contains(fmt.Sprintf("%#v", client), "private-key") || strings.Contains(fmt.Sprintf("%#v", Options{BearerKey: "private-key"}), "private-key") {
		t.Fatalf("secret leaked in error/client string: %v %v", err, client)
	}
}

func TestNonJSONErrorUsesRequestIDHeader(t *testing.T) {
	server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "header-request")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "bad request")
	}))
	defer server.Close()
	client := mustClient(t, Options{BaseURL: server.URL, HTTPClient: httpClient})
	_, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID}, Query: "x"})
	var rejected *RejectedError
	if !errors.As(err, &rejected) || rejected.RequestID != "header-request" || rejected.Code != "" {
		t.Fatalf("error = %#v", err)
	}
}

func TestTimeoutConnectionRefusedAndOversizedResponses(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		defer server.Close()
		client := mustClient(t, Options{BaseURL: server.URL, Timeout: 20 * time.Millisecond, HTTPClient: httpClient})
		_, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID}, Query: "x"})
		close(release)
		var unavailable *UnavailableError
		if !errors.As(err, &unavailable) || unavailable.Reason != "timeout" {
			t.Fatalf("error = %#v, want timeout", err)
		}
	})
	t.Run("connection refused", func(t *testing.T) {
		server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		address := server.URL
		server.Close()
		client := mustClient(t, Options{BaseURL: address, HTTPClient: httpClient})
		_, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID}, Query: "x"})
		var unavailable *UnavailableError
		if !errors.As(err, &unavailable) || unavailable.Reason != "network_error" {
			t.Fatalf("error = %#v, want network error", err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 100)) }))
		defer server.Close()
		client := mustClient(t, Options{BaseURL: server.URL, MaxResponseBytes: 24, HTTPClient: httpClient})
		_, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID}, Query: "x"})
		var unavailable *UnavailableError
		if !errors.As(err, &unavailable) || unavailable.Reason != "response_too_large" {
			t.Fatalf("error = %#v, want oversized response", err)
		}
	})
}

func TestAuthNoneOmitsAuthorizationAndHealthUsesNoAuth(t *testing.T) {
	var authSeen []string
	server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen = append(authSeen, r.Header.Get("Authorization"))
		if r.URL.Path == "/healthz" {
			_, _ = io.WriteString(w, `{"ok":true}`)
			return
		}
		_, _ = io.WriteString(w, `{"hits":[]}`)
	}))
	defer server.Close()
	client := mustClient(t, Options{BaseURL: server.URL, HTTPClient: httpClient})
	if _, err := client.Retrieve(context.Background(), core.MemoryBackendQuery{Space: core.MemorySpace{ID: spaceID}, Query: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(authSeen) != 2 || authSeen[0] != "" || authSeen[1] != "" {
		t.Fatalf("authorization headers = %v", authSeen)
	}
}

func TestServiceOverHTTPAdapterStoresAndRecallsAcrossAgents(t *testing.T) {
	fake := newMemoryAPIFake()
	server, httpClient := newHTTPTestServer(t, fake)
	defer server.Close()
	backend := mustClient(t, Options{BaseURL: server.URL, BearerKey: "key", HTTPClient: httpClient})
	events := make(chan core.Event, 64)
	service := memory.NewService(backend, memory.Config{
		Enabled: true, Workspace: "workspace_00000000000000010000000000000001", WorkspaceSpace: spaceID,
		AuthorizedAgents: []core.AgentID{wraith, veil}, MaxContextTokens: 800, MaxContextRecords: 8,
		WriteQueue: 8, RetrieveTimeout: time.Second, WriteTimeout: time.Second,
	}, func(event core.Event) { events <- event })
	defer func() { _, _ = service.Close(context.Background()) }()
	write := core.MemoryWrite{Scope: core.MemoryScopeWorkspace, Text: "WRAITH found a safe route beside the north bridge", Provenance: core.MemoryProvenance{
		Agent: wraith, Task: "task_00000000000000010000000000000001", Workspace: "project", ClaimKind: core.ClaimInference,
		Confidence: 0.8, Evidence: []string{"event-result-1"}, Validation: core.ValidationSupported,
	}}
	if !service.Enqueue(write) {
		t.Fatal("workspace write was not queued")
	}
	select {
	case <-fake.stored:
	case <-time.After(time.Second):
		t.Fatal("write did not reach fake Ghostdive server")
	}
	got := service.Recall(context.Background(), core.MemoryRecallRequest{
		Query: "safe route", Requester: veil, Scopes: []core.MemoryScope{core.MemoryScopeWorkspace},
	})
	if len(got.Records) != 1 || got.Records[0].Provenance.Agent != wraith || got.Records[0].Provenance.ClaimKind != core.ClaimInference || got.Records[0].Provenance.Evidence[0] != "event-result-1" {
		t.Fatalf("VEIL recall = %#v", got.Records)
	}
	if service.Enqueue(core.MemoryWrite{Scope: core.MemoryScopeAgent, Text: "private", Provenance: core.MemoryProvenance{Agent: wraith, ClaimKind: core.ClaimObservation}}) {
		t.Fatal("agent-scope write without an agent space was queued")
	}
	fake.setOutage(true)
	outage := service.Recall(context.Background(), core.MemoryRecallRequest{
		Query: "safe route", Requester: veil, Scopes: []core.MemoryScope{core.MemoryScopeWorkspace},
	})
	if outage.Context != "" || len(outage.Records) != 0 {
		t.Fatalf("outage recall was not empty: %#v", outage)
	}
	deadline := time.After(time.Second)
	failed, skipped, unavailableClass := false, false, false
	for !failed {
		select {
		case event := <-events:
			failed = event.Type == core.MemoryEventRetrievalFailed
			if failed {
				var payload map[string]any
				_ = json.Unmarshal(event.Payload, &payload)
				unavailableClass = payload["error_class"] == "unavailable"
			}
			skipped = skipped || event.Type == core.MemoryEventWriteSkipped
		case <-deadline:
			t.Fatal("outage did not emit retrieval_failed")
		}
	}
	if !skipped || !unavailableClass {
		t.Fatalf("outage event class or skipped write missing: skipped=%t unavailable=%t", skipped, unavailableClass)
	}
}

func TestNewFromConfigSelectsDisabledUnavailableAndLiveServices(t *testing.T) {
	var events []core.Event
	sink := func(event core.Event) { events = append(events, event) }
	disabled := NewFromConfig(config.MemoryConfig{}, "workspace_00000000000000010000000000000001", nil, sink)
	if len(events) != 0 {
		t.Fatalf("disabled constructor emitted events: %#v", events)
	}
	_ = disabled

	active := config.MemoryConfig{Enabled: true}
	active.Ghostdive.Auth = "none"
	active.Ghostdive.URLBase = "http://127.0.0.1:1"
	service := NewFromConfig(active, "workspace_00000000000000010000000000000001", nil, sink)
	if len(events) != 1 || events[0].Type != core.MemoryEventUnavailable || !strings.Contains(string(events[0].Payload), "workspace_space_missing") {
		t.Fatalf("unavailable selection events = %#v", events)
	}
	_ = service
	active.Ghostdive.SpaceID = "not-a-uuid"
	service = NewFromConfig(active, "workspace_00000000000000010000000000000001", nil, sink)
	if len(events) != 2 || events[1].Type != core.MemoryEventUnavailable || !strings.Contains(string(events[1].Payload), "workspace_space_invalid") {
		t.Fatalf("invalid-space selection events = %#v", events)
	}
	_ = service

	t.Setenv("GHOST_MISSING_KEY", "")
	active.Ghostdive.SpaceID = spaceID
	active.Ghostdive.Auth = "bearer_env"
	active.Ghostdive.Key = "$GHOST_MISSING_KEY"
	service = NewFromConfig(active, "workspace_00000000000000010000000000000001", nil, sink)
	if len(events) != 3 || events[2].Type != core.MemoryEventUnavailable || !strings.Contains(string(events[2].Payload), "bearer_key_missing") {
		t.Fatalf("missing-key selection events = %#v", events)
	}
	_ = service

	server, httpClient := newHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("auth=none sent Authorization: %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"hits":[]}`)
	}))
	defer server.Close()
	active.Ghostdive.Auth, active.Ghostdive.Key, active.Ghostdive.URLBase = "none", "", server.URL
	service = NewFromConfig(active, "workspace_00000000000000010000000000000001", map[string]core.AgentID{"veil": veil}, sink, httpClient)
	_ = service.Recall(context.Background(), core.MemoryRecallRequest{Query: "x", Requester: veil, Scopes: []core.MemoryScope{core.MemoryScopeWorkspace}})
	if len(events) < 3 || events[len(events)-1].Type != core.MemoryEventRetrievalCompleted {
		t.Fatalf("live selection events = %#v", events)
	}
	_, _ = service.Close(context.Background())
}

func mustClient(t *testing.T, options Options) *Client {
	t.Helper()
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func newHTTPTestServer(t *testing.T, handler http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	listener := &pipeListener{connections: make(chan net.Conn), closed: make(chan struct{}), address: inMemoryAddr("ghostdive.test:80")}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	transport := &http.Transport{DialContext: listener.DialContext}
	client := &http.Client{Transport: transport}
	t.Cleanup(func() {
		server.Close()
		transport.CloseIdleConnections()
	})
	return server, client
}

type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	address     net.Addr
	once        sync.Once
}

func (listener *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *pipeListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (listener *pipeListener) Addr() net.Addr { return listener.address }

func (listener *pipeListener) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case listener.connections <- server:
		return client, nil
	case <-listener.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	}
}

type inMemoryAddr string

func (address inMemoryAddr) Network() string { return "tcp" }
func (address inMemoryAddr) String() string  { return string(address) }

func reflectStrings(got any, want []string) bool {
	if values, ok := got.([]string); ok {
		if len(values) != len(want) {
			return false
		}
		for i, value := range values {
			if value != want[i] {
				return false
			}
		}
		return true
	}
	values, ok := got.([]any)
	if !ok || len(values) != len(want) {
		return false
	}
	for i, item := range values {
		if item != want[i] {
			return false
		}
	}
	return true
}

func reflectJSON(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func hasEvent(events <-chan core.Event, eventType string) bool {
	for {
		select {
		case event := <-events:
			if event.Type == eventType {
				return true
			}
		default:
			return false
		}
	}
}

type memoryAPIFake struct {
	mu     sync.Mutex
	items  []map[string]any
	stored chan struct{}
	outage bool
}

func newMemoryAPIFake() *memoryAPIFake        { return &memoryAPIFake{stored: make(chan struct{}, 1)} }
func (f *memoryAPIFake) setOutage(value bool) { f.mu.Lock(); f.outage = value; f.mu.Unlock() }

func (f *memoryAPIFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/spaces/"+spaceID+"/memories" && r.URL.Path != "/v1/spaces/"+spaceID+"/memories/retrieve" {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	outage := f.outage
	f.mu.Unlock()
	if outage {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"cannot open","code":"cannot_open","request_id":"fake-500"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/v1/spaces/"+spaceID+"/memories" {
		var body observeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		item := map[string]any{"id": "memory-1", "layer": body.Layer, "summary": body.Summary, "confidence": body.Confidence, "agent_id": body.AgentID, "promotion_state": "observed", "provenance": body.Provenance, "evidence_refs": body.EvidenceRefs, "created_at": time.Now().UTC().Format(time.RFC3339Nano)}
		f.mu.Lock()
		f.items = append(f.items, item)
		f.mu.Unlock()
		select {
		case f.stored <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(item)
		return
	}
	var query retrieveRequest
	if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	items := append([]map[string]any(nil), f.items...)
	f.mu.Unlock()
	hits := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(fmt.Sprint(item["summary"])), strings.ToLower(query.Query)) {
			hit := make(map[string]any, len(item)+1)
			for key, value := range item {
				hit[key] = value
			}
			hit["score"] = 1.0
			hits = append(hits, hit)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"hits": hits})
}

func envelopeJSON(t *testing.T, value envelope) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
