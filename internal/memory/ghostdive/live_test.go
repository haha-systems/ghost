//go:build live

package ghostdive

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/haha-systems/ghost/internal/core"
)

// TestLiveGhostdiveReadOnly talks to an already-running Ghostdive API. It is
// read-only and refuses non-loopback URLs, so it can never write to or create
// a cloud Ghostdive resource.
//
//	GHOST_LIVE=1                         required
//	GHOST_LIVE_GHOSTDIVE_URL             default http://127.0.0.1:8080
//	GHOSTDIVE_API_KEY                    bearer key for authenticated reads (never logged)
//	GHOST_LIVE_GHOSTDIVE_SPACE           space ID for the read-only retrieve
//	GHOST_LIVE_GHOSTDIVE_RETRIEVE=1      opt in to retrieve; Ghost asks for
//	                                     hybrid mode, which may make the server
//	                                     call its embedding provider
func TestLiveGhostdiveReadOnly(t *testing.T) {
	if os.Getenv("GHOST_LIVE") != "1" {
		t.Skip("live smoke test: set GHOST_LIVE=1 (and -tags live) to run against a local Ghostdive API")
	}
	base := os.Getenv("GHOST_LIVE_GHOSTDIVE_URL")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		t.Fatalf("refusing non-loopback Ghostdive URL %q", base)
	}
	client, err := New(Options{BaseURL: base, BearerKey: os.Getenv("GHOSTDIVE_API_KEY"), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	t.Logf("health ok at %s", base)
	space := os.Getenv("GHOST_LIVE_GHOSTDIVE_SPACE")
	if os.Getenv("GHOSTDIVE_API_KEY") == "" || space == "" || os.Getenv("GHOST_LIVE_GHOSTDIVE_RETRIEVE") != "1" {
		t.Log("authenticated retrieve skipped: needs GHOSTDIVE_API_KEY, GHOST_LIVE_GHOSTDIVE_SPACE and GHOST_LIVE_GHOSTDIVE_RETRIEVE=1")
		return
	}
	records, err := client.Retrieve(ctx, core.MemoryBackendQuery{Space: core.MemorySpace{ID: space, Scope: core.MemoryScopeWorkspace}, Query: "ghost live smoke", TopK: 3})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	t.Logf("retrieve returned %d records", len(records))
}
