package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// maxDiagnosticTail keeps the rendered widget bounded while export retains
	// every record after the most recent clear marker.
	maxDiagnosticTail = 50
	logSchema         = "dobby.log/v1"
)

// DiagnosticHistory separates the bounded human-readable UI tail from the
// complete raw lines used for support export. Raw lines are never populated
// from connection input; they come only from the trusted diagnostic store.
type DiagnosticHistory struct {
	UILines     []string
	ExportLines []string
}

// DiagnosticStore owns retained diagnostic history. Platform shells may
// provide a native implementation (for example, an iOS app-group reader),
// while desktop uses the fixed application-owned files below.
type DiagnosticStore interface {
	Read(context.Context) (DiagnosticHistory, error)
	Clear(context.Context) error
}

// FileDiagnosticStore reads only the product's fixed diagnostic files. The
// primary file belongs to the UI process; producer files are read for history
// and export but are never removed or truncated by Clear.
type FileDiagnosticStore struct {
	primary   string
	producers []string
	base      string

	mu sync.Mutex
}

// NewFileDiagnosticStore returns the desktop store rooted at the user's
// DobbyVPN directory. No caller-supplied path is accepted by this constructor.
func NewFileDiagnosticStore() (*FileDiagnosticStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find user home for diagnostic history: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("user home directory is empty")
	}
	base := filepath.Join(home, ".dobbyvpn")
	primary := filepath.Join(base, "app_logs.txt")
	return &FileDiagnosticStore{
		primary:   primary,
		producers: []string{primary, filepath.Join(base, "go_desktop_service_logs.jsonl")},
		base:      base,
	}, nil
}

// newFileDiagnosticStore is intentionally unexported so tests can exercise
// storage behavior without making production callers select arbitrary paths.
func newFileDiagnosticStore(primary string, producers ...string) *FileDiagnosticStore {
	paths := make([]string, 0, 1+len(producers))
	paths = append(paths, primary)
	paths = append(paths, producers...)
	return &FileDiagnosticStore{
		primary:   filepath.Clean(primary),
		producers: paths,
		base:      filepath.Dir(filepath.Clean(primary)),
	}
}

type unavailableDiagnosticStore struct{ err error }

func (s unavailableDiagnosticStore) Read(ctx context.Context) (DiagnosticHistory, error) {
	if err := contextError(ctx); err != nil {
		return DiagnosticHistory{}, err
	}
	if s.err == nil {
		return DiagnosticHistory{}, errors.New("diagnostic storage is unavailable")
	}
	return DiagnosticHistory{}, fmt.Errorf("diagnostic storage is unavailable: %w", s.err)
}

func (s unavailableDiagnosticStore) Clear(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if s.err == nil {
		return errors.New("diagnostic storage is unavailable")
	}
	return fmt.Errorf("diagnostic storage is unavailable: %w", s.err)
}

// Read returns the records after the latest clear marker. A read error from
// one producer is returned together with any history that could be recovered;
// callers can therefore preserve the visible tail while reporting the
// storage failure honestly.
func (s *FileDiagnosticStore) Read(ctx context.Context) (DiagnosticHistory, error) {
	if err := contextError(ctx); err != nil {
		return DiagnosticHistory{}, err
	}
	if s == nil {
		return DiagnosticHistory{}, errors.New("diagnostic store is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	all := make([]storedDiagnosticRecord, 0)
	var readErrors []error
	for producerIndex, path := range s.producers {
		if err := contextError(ctx); err != nil {
			return diagnosticHistoryFromRecords(all), err
		}
		records, err := readDiagnosticRecords(path, producerIndex)
		if err != nil {
			readErrors = append(readErrors, fmt.Errorf("read diagnostic history %s: %w", filepath.Base(path), err))
		}
		all = append(all, records...)
	}

	filtered := recordsAfterClear(all)
	history := diagnosticHistoryFromRecords(filtered)
	if len(readErrors) > 0 {
		return history, errors.Join(readErrors...)
	}
	return history, nil
}

// Clear truncates only the UI-owned primary file and writes a timestamped
// marker. Producer files remain untouched; their older records are hidden by
// the marker when the merged history is read again.
func (s *FileDiagnosticStore) Clear(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if s == nil {
		return errors.New("diagnostic store is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validatePrimaryPath(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.primary), 0o700); err != nil {
		return fmt.Errorf("create diagnostic directory: %w", err)
	}
	_ = os.Chmod(filepath.Dir(s.primary), 0o700)

	file, err := os.OpenFile(s.primary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open diagnostic history for clear: %w", err)
	}
	marker := map[string]any{
		"schema":    logSchema,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"level":     "INFO",
		"source":    "go-ui",
		"event":     "logs.cleared",
		"message":   "Earlier diagnostic events were cleared from this view",
	}
	encoded, marshalErr := json.Marshal(marker)
	var writeErr error
	if marshalErr == nil {
		_, writeErr = file.Write(append(encoded, '\n'))
		if writeErr == nil {
			writeErr = file.Sync()
		}
	}
	closeErr := file.Close()
	if marshalErr != nil {
		return fmt.Errorf("encode diagnostic clear marker: %w", marshalErr)
	}
	if writeErr != nil {
		return fmt.Errorf("write diagnostic clear marker: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close diagnostic history after clear: %w", closeErr)
	}
	_ = os.Chmod(s.primary, 0o600)
	return nil
}

func (s *FileDiagnosticStore) validatePrimaryPath() error {
	primary := filepath.Clean(s.primary)
	base := filepath.Clean(s.base)
	if primary == "." || base == "." || !filepath.IsAbs(primary) || !filepath.IsAbs(base) {
		return errors.New("diagnostic paths must be absolute")
	}
	relative, err := filepath.Rel(base, primary)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("diagnostic primary file is outside its owned directory")
	}
	parent := filepath.Dir(primary)
	info, err := os.Lstat(parent)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect diagnostic directory: %w", err)
	}
	if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("diagnostic directory is not a real directory")
	}
	info, err = os.Lstat(primary)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect diagnostic history: %w", err)
	}
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("diagnostic history is not a regular file")
	}
	return nil
}

type storedDiagnosticRecord struct {
	timestamp   time.Time
	timestamped bool
	event       string
	producer    int
	recordIndex int
	lines       []string
}

func readDiagnosticRecords(path string, producer int) ([]storedDiagnosticRecord, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// A service diagnostic may contain a large serialized failure. Keep a
	// bounded but generous line limit; malformed oversized records are reported
	// by Scanner rather than silently truncated.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	records := make([]storedDiagnosticRecord, 0)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(records) > 0 && !strings.HasPrefix(line, "{") {
			last := &records[len(records)-1]
			last.lines = append(last.lines, line)
			continue
		}
		timestamp, timestamped, event := diagnosticLineMetadata(line)
		records = append(records, storedDiagnosticRecord{
			timestamp:   timestamp,
			timestamped: timestamped,
			event:       event,
			producer:    producer,
			recordIndex: len(records),
			lines:       []string{line},
		})
	}
	if err := scanner.Err(); err != nil {
		return records, err
	}
	return records, nil
}

func recordsAfterClear(records []storedDiagnosticRecord) []storedDiagnosticRecord {
	var marker *storedDiagnosticRecord
	for index := range records {
		value := records[index]
		if value.producer == 0 && value.event == "logs.cleared" {
			copy := value
			marker = &copy
		}
	}
	if marker == nil {
		return records
	}
	filtered := make([]storedDiagnosticRecord, 0, len(records))
	for _, value := range records {
		if marker.timestamped {
			// Mobile native producers use the shared JSONL schema. Legacy plain
			// lines have no safe ordering information, so treat them as older
			// than a durable clear marker instead of leaking pre-clear history
			// back into the view after an upgrade.
			if !value.timestamped || value.timestamp.Before(marker.timestamp) {
				continue
			}
		} else if value.producer == marker.producer && value.recordIndex < marker.recordIndex {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered
}

func diagnosticHistoryFromRecords(records []storedDiagnosticRecord) DiagnosticHistory {
	ordered := append([]storedDiagnosticRecord(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if left.timestamped == right.timestamped &&
			(!left.timestamped || left.timestamp.Equal(right.timestamp)) {
			if left.producer == right.producer {
				return left.recordIndex < right.recordIndex
			}
			return left.producer < right.producer
		}
		if !left.timestamped {
			return true
		}
		if !right.timestamped {
			return false
		}
		return left.timestamp.Before(right.timestamp)
	})

	raw := make([]string, 0)
	readable := make([]string, 0)
	for _, record := range ordered {
		raw = append(raw, record.lines...)
		// The marker is retained in an export so support can see where the
		// user cleared history, but it is storage metadata rather than a new
		// diagnostic event. A successful clear with no newer events therefore
		// leaves the visible history empty.
		if record.event == "logs.cleared" {
			continue
		}
		for _, line := range record.lines {
			readable = append(readable, renderDiagnosticLine(line))
		}
	}
	if len(readable) > maxDiagnosticTail {
		readable = readable[len(readable)-maxDiagnosticTail:]
	}
	return DiagnosticHistory{UILines: rawCopy(readable), ExportLines: rawCopy(raw)}
}

func rawCopy(values []string) []string { return append([]string(nil), values...) }

func diagnosticLineMetadata(line string) (timestamp time.Time, timestamped bool, event string) {
	if !strings.HasPrefix(line, "{") {
		timestamp, timestamped = legacyDiagnosticTimestamp(line)
		return timestamp, timestamped, ""
	}
	var value struct {
		Timestamp string `json:"timestamp"`
		Event     string `json:"event"`
	}
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return time.Time{}, false, ""
	}
	timestamp, timestamped = parseDiagnosticTimestamp(value.Timestamp)
	return timestamp, timestamped, value.Event
}

func parseDiagnosticTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func legacyDiagnosticTimestamp(line string) (time.Time, bool) {
	if len(line) < 21 || line[0] != '[' || line[20] != ']' {
		return time.Time{}, false
	}
	value := line[1:20]
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func renderDiagnosticLine(line string) string {
	if !strings.HasPrefix(line, "{") {
		return line
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return line
	}
	if schema, _ := value["schema"].(string); schema != "" && schema != logSchema {
		return line
	}
	timestamp, _ := value["timestamp"].(string)
	if len(timestamp) > 23 {
		timestamp = timestamp[:23]
	}
	timestamp = strings.Replace(strings.TrimSuffix(timestamp, "Z"), "T", " ", 1)
	level, _ := value["level"].(string)
	source, _ := value["source"].(string)
	message, _ := value["message"].(string)
	if message == "" {
		return line
	}
	parts := make([]string, 0)
	if fields, ok := value["fields"].(map[string]any); ok {
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", key, fields[key]))
		}
	}
	for key, raw := range value {
		switch key {
		case "schema", "timestamp", "level", "source", "event", "category", "message", "fields":
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", key, raw))
	}
	sort.Strings(parts)
	details := ""
	if len(parts) > 0 {
		details = " · " + strings.Join(parts, " · ")
	}
	prefix := ""
	if timestamp != "" {
		prefix += "[" + timestamp + "] "
	}
	if level != "" {
		prefix += "[" + level + "] "
	}
	if source != "" {
		prefix += "[" + source + "] "
	}
	return prefix + message + details
}
