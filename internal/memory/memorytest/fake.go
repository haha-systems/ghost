// Package memorytest provides a small deterministic in-memory backend.
package memorytest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/haha-systems/ghost/internal/core"
)

var ErrNotFound = errors.New("memory record not found")

// Backend keeps records by space and supports controlled failures and delays.
type Backend struct {
	Latency     time.Duration
	RetrieveErr error
	ObserveErr  error
	GetErr      error
	ObserveGate chan struct{}
	Started     chan struct{}

	mu               sync.Mutex
	spaces           map[string][]core.MemoryRecord
	retrieveRequests []core.MemoryBackendQuery
	writeRequests    []core.MemoryBackendWrite
	getRequests      []GetRequest
	nextID           uint64
}

// GetRequest records one space-scoped record read.
type GetRequest struct {
	Space core.MemorySpace
	ID    string
}

// New returns an empty fake backend.
func New() *Backend { return &Backend{spaces: make(map[string][]core.MemoryRecord)} }

// RetrieveRecords returns a detached copy of the requests seen by Retrieve.
func (b *Backend) RetrieveRecords() []core.MemoryBackendQuery {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]core.MemoryBackendQuery(nil), b.retrieveRequests...)
}

// WriteRecords returns a detached copy of the requests seen by Observe.
func (b *Backend) WriteRecords() []core.MemoryBackendWrite {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]core.MemoryBackendWrite(nil), b.writeRequests...)
}

// GetRecords returns a detached copy of the spaces seen by Get.
func (b *Backend) GetRecords() []GetRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]GetRequest(nil), b.getRequests...)
}

// Records returns all records held in one isolated space.
func (b *Backend) Records(spaceID string) []core.MemoryRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	records := b.spaces[spaceID]
	copy := make([]core.MemoryRecord, len(records))
	for i, record := range records {
		copy[i] = cloneRecord(record)
	}
	return copy
}

// Retrieve performs deterministic token-overlap matching for tests only.
func (b *Backend) Retrieve(ctx context.Context, query core.MemoryBackendQuery) ([]core.MemoryRecord, error) {
	b.mu.Lock()
	b.retrieveRequests = append(b.retrieveRequests, query)
	records := b.spaces[query.Space.ID]
	copy := make([]core.MemoryRecord, len(records))
	for i, record := range records {
		copy[i] = cloneRecord(record)
	}
	b.mu.Unlock()
	if err := b.wait(ctx, false); err != nil {
		return nil, err
	}
	if b.RetrieveErr != nil {
		return nil, b.RetrieveErr
	}
	queryTokens := tokens(query.Query)
	matched := make([]core.MemoryRecord, 0, len(copy))
	for _, record := range copy {
		overlap := 0
		recordTokens := tokens(record.Text + " " + record.Subject)
		for token := range queryTokens {
			if _, exists := recordTokens[token]; exists {
				overlap++
			}
		}
		if len(queryTokens) == 0 || overlap == 0 {
			continue
		}
		record.Score = float64(overlap) / float64(len(queryTokens))
		matched = append(matched, record)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].Score != matched[j].Score {
			return matched[i].Score > matched[j].Score
		}
		return matched[i].ID < matched[j].ID
	})
	if query.TopK > 0 && len(matched) > query.TopK {
		matched = matched[:query.TopK]
	}
	return matched, nil
}

// Observe appends a record. It never replaces a record with a matching topic.
func (b *Backend) Observe(ctx context.Context, request core.MemoryBackendWrite) (core.MemoryRecord, error) {
	b.mu.Lock()
	b.writeRequests = append(b.writeRequests, cloneWrite(request))
	b.mu.Unlock()
	if err := b.wait(ctx, true); err != nil {
		return core.MemoryRecord{}, err
	}
	if b.ObserveErr != nil {
		return core.MemoryRecord{}, b.ObserveErr
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spaces == nil {
		b.spaces = make(map[string][]core.MemoryRecord)
	}
	b.nextID++
	record := cloneRecord(request.Record)
	if record.ID == "" {
		record.ID = fmt.Sprintf("memory-%06d", b.nextID)
	}
	b.spaces[request.Space.ID] = append(b.spaces[request.Space.ID], record)
	return cloneRecord(record), nil
}

// Get reads a record only from the supplied space.
func (b *Backend) Get(ctx context.Context, space core.MemorySpace, id string) (core.MemoryRecord, error) {
	b.mu.Lock()
	b.getRequests = append(b.getRequests, GetRequest{Space: space, ID: id})
	records := b.spaces[space.ID]
	copy := make([]core.MemoryRecord, len(records))
	for i, record := range records {
		copy[i] = cloneRecord(record)
	}
	b.mu.Unlock()
	if err := b.wait(ctx, false); err != nil {
		return core.MemoryRecord{}, err
	}
	if b.GetErr != nil {
		return core.MemoryRecord{}, b.GetErr
	}
	for _, record := range copy {
		if record.ID == id {
			return record, nil
		}
	}
	return core.MemoryRecord{}, ErrNotFound
}

func (b *Backend) wait(ctx context.Context, observe bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if observe && b.Started != nil {
		select {
		case b.Started <- struct{}{}:
		default:
		}
	}
	if observe && b.ObserveGate != nil {
		select {
		case <-b.ObserveGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if b.Latency <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(b.Latency)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func tokens(value string) map[string]struct{} {
	result := map[string]struct{}{}
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			result[strings.ToLower(current.String())] = struct{}{}
			current.Reset()
		}
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return result
}

func cloneRecord(record core.MemoryRecord) core.MemoryRecord {
	record.Provenance.EventRefs = append([]string(nil), record.Provenance.EventRefs...)
	record.Provenance.ArtifactRefs = append([]string(nil), record.Provenance.ArtifactRefs...)
	record.Provenance.Evidence = append([]string(nil), record.Provenance.Evidence...)
	record.Provenance.Counterevidence = append([]string(nil), record.Provenance.Counterevidence...)
	record.ConflictIDs = append([]string(nil), record.ConflictIDs...)
	return record
}

func cloneWrite(request core.MemoryBackendWrite) core.MemoryBackendWrite {
	request.Record = cloneRecord(request.Record)
	return request
}
