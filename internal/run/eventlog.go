package run

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/haha-systems/ghost/internal/config"
	"github.com/haha-systems/ghost/internal/core"
)

const (
	defaultRecoveryMaxFiles  = 100
	defaultRecoveryMaxEvents = 20000
)

// RecoveryLimits bounds restart replay. Zero fields use the defaults.
type RecoveryLimits struct {
	MaxFiles  int
	MaxEvents int
}

// RecoverySummary is written as one workspace.recovered event on startup.
type RecoverySummary struct {
	FilesRead           int            `json:"files_read"`
	FilesSkipped        int            `json:"files_skipped"`
	FilesOmitted        int            `json:"files_omitted"`
	EventsRead          int            `json:"events_read"`
	EventsOmitted       int            `json:"events_omitted"`
	SkippedTailLines    int            `json:"skipped_tail_lines"`
	MessagesRestored    int            `json:"messages_restored"`
	TasksRestored       int            `json:"tasks_restored"`
	ReconciledTasks     int            `json:"reconciled_tasks"`
	UncertainDeliveries int            `json:"uncertain_deliveries"`
	PendingMessages     int            `json:"pending_messages"`
	Memory              MemoryCounters `json:"memory"`
}

type recoveryHistory struct {
	Events               []core.Event
	Warnings             []string
	ReservedEventIDs     []core.EventID
	WorkspaceID          core.WorkspaceID
	AgentIDs             map[string]core.AgentID
	LastSequences        map[string]uint64
	HasWorkspaceIdentity bool
	Summary              RecoverySummary
}

// ConfiguredWorkDir returns a stable log root for one controller built from a
// multi-agent configuration. Configured agent workdirs are expected to name
// one workspace; when they differ, the first agent by name selects the root.
func ConfiguredWorkDir(cfg config.Config) string {
	if cfg.Agents == nil || len(*cfg.Agents) == 0 {
		return ""
	}
	names := make([]string, 0, len(*cfg.Agents))
	for name := range *cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.TrimSpace((*cfg.Agents)[names[0]].WorkingDir)
}

func openRunEventLog(workdir string) (*core.EventLog, string, error) {
	if workdir == "" {
		return nil, "", fmt.Errorf("work directory is required")
	}
	dir := filepath.Join(workdir, ".ghost", "events")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create event log directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("set event log directory permissions: %w", err)
	}
	for {
		startID, err := core.NewID("session")
		if err != nil {
			return nil, "", fmt.Errorf("make event log session ID: %w", err)
		}
		path := filepath.Join(dir, startID+".jsonl")
		log, err := core.Create(path, true)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("create event log: %w", err)
		}
		return log, path, nil
	}
}

func readWorkspaceEventHistory(workdir string, limits RecoveryLimits) (recoveryHistory, error) {
	history := recoveryHistory{AgentIDs: map[string]core.AgentID{}, LastSequences: map[string]uint64{}}
	if strings.TrimSpace(workdir) == "" {
		return history, nil
	}
	maxFiles, maxEvents := limits.MaxFiles, limits.MaxEvents
	if maxFiles <= 0 {
		maxFiles = defaultRecoveryMaxFiles
	}
	if maxEvents <= 0 {
		maxEvents = defaultRecoveryMaxEvents
	}
	dir := filepath.Join(workdir, ".ghost", "events")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return history, nil
	}
	if err != nil {
		return history, fmt.Errorf("read event log directory: %w", err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	firstReplayFile := max(0, len(files)-maxFiles)
	if firstReplayFile > 0 {
		history.Summary.FilesOmitted = firstReplayFile
		history.Warnings = append(history.Warnings, fmt.Sprintf("recovery omitted %d oldest event files; limit is %d", history.Summary.FilesOmitted, maxFiles))
	}
	var events []core.Event
	seenIDs := map[core.EventID]bool{}
	replayedIDs := map[core.EventID]bool{}
	reservedIDs := map[core.EventID]bool{}
	sequencesByWorkspace := map[core.WorkspaceID]map[string]uint64{}
	omittedEvents := 0
	for fileIndex, name := range files {
		var fileEvents []core.Event
		history.Summary.FilesRead++
		recordMetadata := func(item core.Event) {
			if item.ID.Validate() == nil && !reservedIDs[item.ID] {
				history.ReservedEventIDs = append(history.ReservedEventIDs, item.ID)
				reservedIDs[item.ID] = true
			}
			if item.Validate() == nil {
				sequences := sequencesByWorkspace[item.Workspace]
				if sequences == nil {
					sequences = map[string]uint64{}
					sequencesByWorkspace[item.Workspace] = sequences
				}
				if item.Seq > sequences[item.StreamKey()] {
					sequences[item.StreamKey()] = item.Seq
				}
			}
		}
		path := filepath.Join(dir, name)
		report, replayErr := core.ReadOnlyReplay(path, func(item core.Event) error {
			fileEvents = append(fileEvents, item)
			recordMetadata(item)
			return nil
		})
		if replayErr != nil {
			history.Summary.FilesSkipped++
			history.Warnings = append(history.Warnings, fmt.Sprintf("event file %s skipped: %v", name, replayErr))
			if err := collectEventMetadata(path, recordMetadata); err != nil {
				history.Warnings = append(history.Warnings, fmt.Sprintf("event metadata from %s could not be read: %v", name, err))
			}
			continue
		}
		history.Summary.SkippedTailLines += report.SkippedTailLines
		if report.SkippedTailLines > 0 {
			history.Warnings = append(history.Warnings, fmt.Sprintf("event file %s has %d truncated final line(s); skipped", name, report.SkippedTailLines))
			if err := collectEventMetadata(path, recordMetadata); err != nil {
				history.Warnings = append(history.Warnings, fmt.Sprintf("event metadata from %s could not be read: %v", name, err))
			}
		}
		for _, item := range fileEvents {
			duplicate := seenIDs[item.ID]
			if duplicate {
				history.Warnings = append(history.Warnings, fmt.Sprintf("duplicate event ID %s found in %s; retaining one replay copy", item.ID, name))
			} else {
				seenIDs[item.ID] = true
			}
			if history.WorkspaceID == "" {
				history.WorkspaceID = item.Workspace
			}
			if item.Workspace != history.WorkspaceID {
				history.Warnings = append(history.Warnings, fmt.Sprintf("event %s from workspace %s skipped", item.ID, item.Workspace))
				if !duplicate {
					omittedEvents++
				}
				continue
			}
			captureAgentIdentities(item, history.AgentIDs)
			if item.Type == "workspace.initialized" || item.Type == "workspace.identity_snapshot" {
				history.HasWorkspaceIdentity = true
			}
			if fileIndex < firstReplayFile {
				if !duplicate {
					omittedEvents++
				}
				continue
			}
			if replayedIDs[item.ID] {
				continue
			}
			replayedIDs[item.ID] = true
			events = append(events, item)
		}
	}
	if history.WorkspaceID != "" {
		history.LastSequences = sequencesByWorkspace[history.WorkspaceID]
		if history.LastSequences == nil {
			history.LastSequences = map[string]uint64{}
		}
	}
	if len(events) > maxEvents {
		omittedEvents += len(events) - maxEvents
		events = events[len(events)-maxEvents:]
	}
	history.Summary.EventsOmitted = omittedEvents
	if omittedEvents > 0 {
		history.Warnings = append(history.Warnings, fmt.Sprintf("recovery omitted %d events because of replay limits; event limit is %d", omittedEvents, maxEvents))
	}
	for _, item := range events {
		if item.Workspace == history.WorkspaceID {
			history.Events = append(history.Events, item)
		}
	}
	history.Summary.EventsRead = len(history.Events)
	return history, nil
}

// collectEventMetadata reserves IDs and sequences from valid lines in a file
// that replay must skip after finding corruption. It does not restore records.
func collectEventMetadata(path string, visit func(core.Event)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			raw := strings.TrimSuffix(string(line), "\n")
			raw = strings.TrimSuffix(raw, "\r")
			var item core.Event
			if json.Unmarshal([]byte(raw), &item) == nil {
				visit(item)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func captureAgentIdentities(item core.Event, identities map[string]core.AgentID) {
	if item.Type == "workspace.initialized" || item.Type == "workspace.identity_snapshot" {
		var payload struct {
			Agents []struct {
				Name string       `json:"name"`
				ID   core.AgentID `json:"id"`
			} `json:"agents"`
		}
		if json.Unmarshal(item.Payload, &payload) == nil {
			for _, agent := range payload.Agents {
				if strings.TrimSpace(agent.Name) != "" && agent.ID.Validate() == nil {
					identities[agent.Name] = agent.ID
				}
			}
		}
	}
	var payload struct {
		Message *core.Message `json:"message"`
		Task    *core.Task    `json:"task"`
		Runtime struct {
			AgentID string `json:"agent_id"`
		} `json:"runtime"`
		AgentID string `json:"agent_id"`
	}
	if json.Unmarshal(item.Payload, &payload) == nil {
		name := payload.Runtime.AgentID
		if name == "" {
			name = payload.AgentID
		}
		if name != "" && item.AgentID.Validate() == nil {
			identities[name] = item.AgentID
		}
		if payload.Task != nil {
			if payload.Task.Owner.Validate() == nil {
				identities["\x00agent-id:"+string(payload.Task.Owner)] = payload.Task.Owner
			}
			for _, id := range payload.Task.Participants {
				if id.Validate() == nil {
					identities["\x00agent-id:"+string(id)] = id
				}
			}
		}
		if payload.Message != nil {
			if payload.Message.Sender.Validate() == nil {
				identities["\x00agent-id:"+string(payload.Message.Sender)] = payload.Message.Sender
			}
			for _, id := range payload.Message.Recipients {
				if id.Validate() == nil {
					identities["\x00agent-id:"+string(id)] = id
				}
			}
		}
	}
	if item.AgentID.Validate() == nil {
		identities["\x00agent-id:"+string(item.AgentID)] = item.AgentID
	}
	if item.Actor.Kind == core.ActorAgent {
		id := core.AgentID(item.Actor.ID)
		identities["\x00agent-id:"+string(id)] = id
	}
}
