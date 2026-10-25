// Package ghostdive adapts Ghostdive's documented HTTP API to core.MemoryBackend.
package ghostdive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
	"github.com/haha-systems/ghost/internal/memory"
)

const defaultMaxResponseBytes = 1 << 20

// Options configures the HTTP adapter. BearerKey is held only by Client and is
// never included in its text form, events, or errors.
type Options struct {
	BaseURL          string
	BearerKey        string
	HTTPClient       *http.Client
	Timeout          time.Duration
	MaxResponseBytes int64
}

// String returns a safe summary that does not expose the bearer key.
func (options Options) String() string {
	return fmt.Sprintf("ghostdive.Options{BaseURL:%q, BearerKeySet:%t}", options.BaseURL, options.BearerKey != "")
}

func (options Options) GoString() string { return options.String() }

// Client implements core.MemoryBackend over Ghostdive's public memory routes.
type Client struct {
	baseURL         url.URL
	key             string
	httpClient      *http.Client
	maxResponseSize int64
}

// String reports adapter configuration without showing credentials.
func (client *Client) String() string {
	if client == nil {
		return "ghostdive.Client<nil>"
	}
	return fmt.Sprintf("ghostdive.Client{BaseURL:%q, BearerKeySet:%t}", client.baseURL.String(), client.key != "")
}

func (client *Client) GoString() string { return client.String() }

// New creates an HTTP adapter. It performs no network request.
func New(options Options) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(options.BaseURL))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("ghostdive: invalid base URL")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		timeout := options.Timeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		httpClient = &http.Client{Timeout: timeout}
	} else {
		copy := *httpClient
		if copy.Timeout <= 0 && options.Timeout > 0 {
			copy.Timeout = options.Timeout
		}
		httpClient = &copy
	}
	// A redirect could send a request to an endpoint outside this adapter's
	// allowlist, so return redirects to the caller instead of following them.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	maxResponseSize := options.MaxResponseBytes
	if maxResponseSize <= 0 {
		maxResponseSize = defaultMaxResponseBytes
	}
	return &Client{baseURL: *base, key: options.BearerKey, httpClient: httpClient, maxResponseSize: maxResponseSize}, nil
}

// UnavailableError reports transport, timeout, oversized-response, and 5xx
// failures. Cause text is omitted so credentials cannot enter error strings.
type UnavailableError struct {
	Operation  string
	Reason     string
	StatusCode int
	Code       string
	RequestID  string
}

func (err *UnavailableError) Error() string {
	if err == nil {
		return "ghostdive unavailable"
	}
	if err.StatusCode != 0 {
		return fmt.Sprintf("ghostdive %s unavailable: status=%d code=%q request_id=%q", err.Operation, err.StatusCode, err.Code, err.RequestID)
	}
	return fmt.Sprintf("ghostdive %s unavailable: %s", err.Operation, err.Reason)
}

func (*UnavailableError) ErrorClass() string { return "unavailable" }

// RejectedError reports a non-quota 4xx response.
type RejectedError struct {
	StatusCode int
	Code       string
	RequestID  string
}

func (err *RejectedError) Error() string {
	if err == nil {
		return "ghostdive rejected request"
	}
	return fmt.Sprintf("ghostdive rejected request: status=%d code=%q request_id=%q", err.StatusCode, err.Code, err.RequestID)
}

func (*RejectedError) ErrorClass() string { return "rejected" }

// QuotaExceededError reports a 429 response.
type QuotaExceededError struct {
	StatusCode int
	Code       string
	RequestID  string
}

var _ core.MemoryBackend = (*Client)(nil)

func (err *QuotaExceededError) Error() string {
	if err == nil {
		return "ghostdive quota exceeded"
	}
	return fmt.Sprintf("ghostdive quota exceeded: status=%d code=%q request_id=%q", err.StatusCode, err.Code, err.RequestID)
}

func (*QuotaExceededError) ErrorClass() string { return "quota" }

// Retrieve searches one already-authorised Ghostdive space. Ghost performs
// further scope checks on the decoded provenance envelope.
func (client *Client) Retrieve(ctx context.Context, query core.MemoryBackendQuery) ([]core.MemoryRecord, error) {
	body, err := json.Marshal(retrieveRequest{Query: query.Query, TopK: positiveOr(query.TopK, 5), Mode: "hybrid"})
	if err != nil {
		return nil, &UnavailableError{Operation: "retrieve", Reason: "invalid_request"}
	}
	response, err := client.do(ctx, http.MethodPost, memoryPath(query.Space.ID)+"/retrieve", body, "retrieve")
	if err != nil {
		return nil, err
	}
	var result retrieveResponse
	if err := json.Unmarshal(response, &result); err != nil {
		return nil, &UnavailableError{Operation: "retrieve", Reason: "invalid_response"}
	}
	records := make([]core.MemoryRecord, 0, len(result.Hits))
	for _, hit := range result.Hits {
		records = append(records, decodeRecord(hit.recordDTO, query.Space, hit.Score))
	}
	return records, nil
}

// Observe writes one memory. Every write keeps Ghostdive topic supersession
// disabled. Ghost stores every claim in the episodic layer in this milestone.
func (client *Client) Observe(ctx context.Context, write core.MemoryBackendWrite) (core.MemoryRecord, error) {
	provenance := memory.NormalizeProvenance(write.Record.Provenance)
	text := truncateRunes(write.Record.Text, 4000)
	scope := write.Record.Scope
	if scope == "" {
		scope = write.Space.Scope
	}
	envelopeValue := envelope{
		Version: 1, Scope: scope, Agent: provenance.Agent, Task: provenance.Task,
		Workspace: provenance.Workspace, Claim: provenance.ClaimKind, Validation: provenance.Validation,
		Evidence: copyStrings(provenance.Evidence), Counterevidence: copyStrings(provenance.Counterevidence),
		Events: copyStrings(provenance.EventRefs), Artifacts: copyStrings(provenance.ArtifactRefs), RecordedAt: provenance.RecordedAt,
	}
	note, err := json.Marshal(envelopeValue)
	if err != nil {
		return core.MemoryRecord{}, &UnavailableError{Operation: "observe", Reason: "invalid_request"}
	}
	evidenceRefs := referenceSet(provenance.EventRefs, provenance.ArtifactRefs, provenance.Evidence)
	request := observeRequest{
		Layer: "episodic", Subject: truncateRunes(write.Record.Subject, 200), Summary: text,
		Confidence: clampConfidence(provenance.Confidence), AgentID: string(provenance.Agent),
		Provenance: provenanceDTO{
			SourceKind: "ghost/" + string(provenance.ClaimKind),
			SourceRef:  sourceRef(provenance, scope), SourceRecordID: provenance.Challenges, Note: string(note),
		},
		EvidenceRefs: evidenceRefs, SkipAutoSupersede: true,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return core.MemoryRecord{}, &UnavailableError{Operation: "observe", Reason: "invalid_request"}
	}
	response, err := client.do(ctx, http.MethodPost, memoryPath(write.Space.ID), body, "observe")
	if err != nil {
		return core.MemoryRecord{}, err
	}
	var record recordDTO
	if err := json.Unmarshal(response, &record); err != nil {
		return core.MemoryRecord{}, &UnavailableError{Operation: "observe", Reason: "invalid_response"}
	}
	return decodeRecord(record, write.Space, 0), nil
}

// Get reads a record from the supplied space.
func (client *Client) Get(ctx context.Context, space core.MemorySpace, id string) (core.MemoryRecord, error) {
	response, err := client.do(ctx, http.MethodGet, memoryPath(space.ID)+"/"+url.PathEscape(id), nil, "get")
	if err != nil {
		return core.MemoryRecord{}, err
	}
	var record recordDTO
	if err := json.Unmarshal(response, &record); err != nil {
		return core.MemoryRecord{}, &UnavailableError{Operation: "get", Reason: "invalid_response"}
	}
	return decodeRecord(record, space, 0), nil
}

// Health checks the documented unauthenticated /healthz route.
func (client *Client) Health(ctx context.Context) error {
	response, err := client.doHealth(ctx)
	if err != nil {
		return err
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(response, &result); err != nil || !result.OK {
		return &UnavailableError{Operation: "health", Reason: "invalid_response"}
	}
	return nil
}

func (client *Client) doHealth(ctx context.Context) ([]byte, error) {
	return client.doWithAuth(ctx, http.MethodGet, "/healthz", nil, "health", false)
}

func (client *Client) do(ctx context.Context, method, path string, body []byte, operation string) ([]byte, error) {
	return client.doWithAuth(ctx, method, path, body, operation, true)
}

func (client *Client) doWithAuth(ctx context.Context, method, path string, body []byte, operation string, authenticate bool) ([]byte, error) {
	if client == nil || client.httpClient == nil {
		return nil, &UnavailableError{Operation: operation, Reason: "client_unconfigured"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestURL, err := client.endpoint(path)
	if err != nil {
		return nil, &UnavailableError{Operation: operation, Reason: "invalid_request"}
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, &UnavailableError{Operation: operation, Reason: "invalid_request"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "ghost")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticate && client.key != "" {
		request.Header.Set("Authorization", "Bearer "+client.key)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		reason := "network_error"
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			reason = "timeout"
		} else if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			reason = "cancelled"
		}
		return nil, &UnavailableError{Operation: operation, Reason: reason}
	}
	defer func() { _ = response.Body.Close() }()
	data, tooLarge, readErr := readBounded(response.Body, client.maxResponseSize)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, mapHTTPError(response, data, tooLarge, client.key, operation)
	}
	if readErr != nil {
		return nil, &UnavailableError{Operation: operation, Reason: "response_read_error"}
	}
	if tooLarge {
		return nil, &UnavailableError{Operation: operation, Reason: "response_too_large"}
	}
	return data, nil
}

func (client *Client) endpoint(path string) (string, error) {
	if client == nil || path == "" || !strings.HasPrefix(path, "/") {
		return "", errors.New("invalid endpoint")
	}
	base := client.baseURL
	escapedPath := strings.TrimRight(base.EscapedPath(), "/") + path
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", err
	}
	base.Path = decodedPath
	base.RawPath = escapedPath
	return base.String(), nil
}

func memoryPath(spaceID string) string {
	return "/v1/spaces/" + url.PathEscape(spaceID) + "/memories"
}

func mapHTTPError(response *http.Response, body []byte, tooLarge bool, key, operation string) error {
	code, requestID := "", response.Header.Get("X-Request-Id")
	if !tooLarge {
		var payload struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(body, &payload) == nil {
			code = payload.Code
			if payload.RequestID != "" {
				requestID = payload.RequestID
			}
		}
	}
	code, requestID = redact(code, key), redact(requestID, key)
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return &UnavailableError{Operation: operation, Reason: "unexpected_redirect", StatusCode: response.StatusCode, Code: code, RequestID: requestID}
	case response.StatusCode == http.StatusTooManyRequests:
		return &QuotaExceededError{StatusCode: response.StatusCode, Code: code, RequestID: requestID}
	case response.StatusCode >= 500:
		return &UnavailableError{Operation: operation, StatusCode: response.StatusCode, Code: code, RequestID: requestID}
	default:
		return &RejectedError{StatusCode: response.StatusCode, Code: code, RequestID: requestID}
	}
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	return data, int64(len(data)) > limit, err
}

func redact(value, secret string) string {
	if secret == "" || value == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "[redacted]")
}

func positiveOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func clampConfidence(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0.01 {
		return 0.01
	}
	if value > 0.9 {
		return 0.9
	}
	return value
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func copyStrings(values []string) []string { return append([]string(nil), values...) }

func referenceSet(groups ...[]string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func sourceRef(provenance core.MemoryProvenance, scope core.MemoryScope) string {
	owner := string(provenance.Agent)
	if owner == "" {
		owner = string(provenance.Task)
	}
	return fmt.Sprintf("ghost://%s/%s/%s", provenance.Workspace, scope, owner)
}

// NewFromConfig builds a disabled, unavailable, or live service without doing
// network work. agentIDs maps configured agent names to stable runtime IDs.
func NewFromConfig(settings config.MemoryConfig, workspace core.WorkspaceID, agentIDs map[string]core.AgentID, sink memory.EventSink, clients ...*http.Client) *memory.Service {
	serviceConfig := memory.Config{
		Enabled: settings.Enabled, Workspace: workspace,
		WorkspaceSpace: settings.Ghostdive.SpaceID, GlobalSpace: settings.Ghostdive.GlobalSpaceID,
		ShareGlobal: settings.ShareGlobal, MaxContextTokens: settings.MaxContextTokens,
		MaxContextRecords: settings.MaxContextRecords, RetrieveTimeout: durationOr(settings.RetrieveTimeout, 2*time.Second),
		WriteTimeout: durationOr(settings.WriteTimeout, 5*time.Second), WriteQueue: settings.WriteQueue,
	}
	if len(settings.Ghostdive.AgentSpaces) > 0 {
		serviceConfig.AgentSpaces = make(map[core.AgentID]string, len(settings.Ghostdive.AgentSpaces))
	}
	for name, id := range agentIDs {
		serviceConfig.AuthorizedAgents = append(serviceConfig.AuthorizedAgents, id)
		if id == "" {
			continue
		}
		if space := settings.Ghostdive.AgentSpaces[name]; space != "" {
			serviceConfig.AgentSpaces[id] = space
		}
	}
	for name, space := range settings.Ghostdive.AgentSpaces {
		if agentIDs[name] == "" {
			id := core.AgentID(name)
			serviceConfig.AgentSpaces[id] = space
			serviceConfig.AuthorizedAgents = append(serviceConfig.AuthorizedAgents, id)
		}
	}
	if !settings.Enabled {
		return memory.NewService(nil, serviceConfig, sink)
	}
	if settings.Ghostdive.SpaceID == "" {
		serviceConfig.UnavailableReason = "workspace_space_missing"
		return memory.NewService(nil, serviceConfig, sink)
	}
	if !validUUID(settings.Ghostdive.SpaceID) {
		serviceConfig.UnavailableReason = "workspace_space_invalid"
		return memory.NewService(nil, serviceConfig, sink)
	}
	key, reason := resolveKey(settings.Ghostdive.Auth, settings.Ghostdive.Key)
	if reason != "" {
		serviceConfig.UnavailableReason = reason
		return memory.NewService(nil, serviceConfig, sink)
	}
	var httpClient *http.Client
	if len(clients) > 0 {
		httpClient = clients[0]
	}
	client, err := New(Options{BaseURL: settings.Ghostdive.URLBase, BearerKey: key, HTTPClient: httpClient})
	if err != nil {
		serviceConfig.UnavailableReason = "backend_config_invalid"
		return memory.NewService(nil, serviceConfig, sink)
	}
	return memory.NewService(client, serviceConfig, sink)
}

func resolveKey(auth, configured string) (string, string) {
	switch auth {
	case "none":
		if configured != "" {
			return "", "auth_config_invalid"
		}
		return "", ""
	case "bearer_string":
		if configured == "" {
			return "", "bearer_key_missing"
		}
		return configured, ""
	case "bearer_env":
		if !strings.HasPrefix(configured, "$") || len(configured) == 1 {
			return "", "bearer_env_invalid"
		}
		value, ok := os.LookupEnv(strings.TrimPrefix(configured, "$"))
		if !ok || value == "" {
			return "", "bearer_key_missing"
		}
		return value, ""
	default:
		return "", "auth_config_invalid"
	}
}

func durationOr(value string, fallback time.Duration) time.Duration {
	if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, char := range value {
		switch i {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
				return false
			}
		}
	}
	return true
}
