package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// The lifecycle timeline is one JSON line per stage boundary. A record carries
// both wall-clock UTC time and monotonic elapsed milliseconds, the process
// role, and the attempt it belongs to, so a failed transition can be read
// without correlating free-text log lines.
const (
	// TimelineFileName is the retained timeline beside the process logs.
	TimelineFileName = "timeline.jsonl"
	// TimelineEvent marks every stage record in the structured log as well.
	TimelineEvent = "lifecycle.stage"
	// TimelineCategory groups the mirrored log records.
	TimelineCategory = "TIMELINE"
)

// Process roles. These name who is speaking, never a machine or device.
const (
	RoleBackend   = "backend"
	RoleCLI       = "cli"
	RoleIOSApp    = "ios-app"
	RoleIOSTunnel = "ios-tunnel"
	RoleAndroid   = "android-app"
)

// Timeline appends stage records to dir/timeline.jsonl. A nil or unconfigured
// timeline is a no-op so call sites stay safe before initialization.
type Timeline struct {
	dir string
	mu  sync.Mutex
}

var (
	timelineHandle atomic.Pointer[Timeline]
	currentAttempt atomic.Pointer[string]
	currentRole    atomic.Pointer[string]
)

// SetTimelineDir installs the process timeline beside its retained logs. It is
// called once from each producer's logger initialization.
func SetTimelineDir(dir string) {
	if dir == "" {
		timelineHandle.Store(nil)
		return
	}
	timelineHandle.Store(&Timeline{dir: dir})
}

// TimelineDir reports the installed directory, or "" when unconfigured.
func TimelineDir() string {
	handle := timelineHandle.Load()
	if handle == nil {
		return ""
	}
	return handle.dir
}

// SetRole records which process is writing. Producers set it once at startup.
func SetRole(role string) {
	value := role
	currentRole.Store(&value)
}

// Role reports the installed process role, or "" when unconfigured.
func Role() string {
	value := currentRole.Load()
	if value == nil {
		return ""
	}
	return *value
}

// NewAttemptID returns 32 lowercase hex characters, one per start attempt. It
// is never persisted outside its own timeline records.
func NewAttemptID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// The runtime's CSPRNG does not fail on the supported platforms; keep a
		// distinct value rather than an empty correlation field.
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(raw[:])
}

// SetAttemptID stores the attempt the current goroutine's process is serving.
func SetAttemptID(id string) {
	value := id
	currentAttempt.Store(&value)
}

// CurrentAttemptID reports the stored attempt, or "" when unset.
func CurrentAttemptID() string {
	value := currentAttempt.Load()
	if value == nil {
		return ""
	}
	return *value
}

// Path is the retained timeline file, or "" when unconfigured.
func (t *Timeline) Path() string {
	if t == nil {
		return ""
	}
	return filepath.Join(t.dir, TimelineFileName)
}

// BeginStage writes the begin record and returns the matching end writer. When
// no timeline is installed both calls do nothing and the returned function is
// safe to call.
func BeginStage(attemptID, operation, stage, role string) (end func(result string)) {
	timeline := timelineHandle.Load()
	if timeline == nil {
		return func(string) {}
	}
	started := time.Now()
	if err := timeline.append(stageRecord(attemptID, operation, stage, role, "begin", "", nil)); err != nil {
		reportTimelineFailure(err)
	}
	return func(result string) {
		elapsed := time.Since(started).Milliseconds()
		if err := timeline.append(stageRecord(attemptID, operation, stage, role, "end", result, &elapsed)); err != nil {
			reportTimelineFailure(err)
		}
	}
}

// Mark writes one stage event that has no begin/end pair.
func Mark(attemptID, operation, stage, role, result string) {
	timeline := timelineHandle.Load()
	if timeline == nil {
		return
	}
	if err := timeline.append(stageRecord(attemptID, operation, stage, role, "mark", result, nil)); err != nil {
		reportTimelineFailure(err)
	}
}

// StageRecord is the exact on-disk shape. Field names are part of the export
// contract the iOS emitter mirrors.
type StageRecord struct {
	OccurredAt string `json:"occurred_at"`
	Event      string `json:"event"`
	AttemptID  string `json:"attempt_id"`
	Operation  string `json:"operation"`
	Stage      string `json:"stage"`
	Role       string `json:"role"`
	Phase      string `json:"phase"`
	Result     string `json:"result,omitempty"`
	ElapsedMS  *int64 `json:"elapsed_ms,omitempty"`
}

func stageRecord(attemptID, operation, stage, role, phase, result string, elapsed *int64) StageRecord {
	if attemptID == "" {
		attemptID = CurrentAttemptID()
	}
	if attemptID == "" {
		// A record without a correlation field would be unusable next to its
		// peers, so a producer that never minted one gets a per-process
		// identity instead of an empty string.
		attemptID = processAttempt()
	}
	if role == "" {
		role = Role()
	}
	return StageRecord{
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Event:      TimelineEvent,
		AttemptID:  attemptID,
		Operation:  operation,
		Stage:      stage,
		Role:       role,
		Phase:      phase,
		Result:     result,
		ElapsedMS:  elapsed,
	}
}

func (t *Timeline) append(record StageRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	t.mu.Lock()
	defer t.mu.Unlock()
	writer, err := OpenWriter(t.Path())
	if err != nil {
		return err
	}
	if _, err := writer.Write(payload); err != nil {
		return err
	}
	return writer.Close()
}

var timelineFailure sync.Once

// reportTimelineFailure records one write failure per process. The timeline is
// a diagnostic: a broken destination must never fail the lifecycle it watches,
// and repeating the same warning would drown the logs it is meant to help.
func reportTimelineFailure(err error) {
	if err == nil {
		return
	}
	timelineFailure.Do(func() {
		_, _ = fmt.Fprintf(Stderr, "lifecycle timeline write failed: %v\n", err)
	})
}

var fallbackAttempt struct {
	once sync.Once
	id   string
}

// processAttempt returns one stable identity for producers that never mint a
// per-attempt ID, so every stage record stays correlatable.
func processAttempt() string {
	fallbackAttempt.once.Do(func() { fallbackAttempt.id = NewAttemptID() })
	return fallbackAttempt.id
}
