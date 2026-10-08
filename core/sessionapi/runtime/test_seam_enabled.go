//go:build dobbyvpn_test_seams

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"core/log"
	"core/sessionapi"
)

// These product-owned seams are compiled only into a build-local qualification
// binary. Release builds do not read the variables or contain the injectors.
const (
	testHealthFaultAfterEnv   = "DOBBYVPN_TEST_HEALTH_FAULT_AFTER_SUCCESSFUL_CHECKS"
	testRecoveryStopMarkerEnv = "DOBBYVPN_TEST_RECOVERY_STOP_MARKER"
)

// TestRecoveryStopMarker can be set in tagged build-local binaries with
// `-X core/sessionapi/runtime.TestRecoveryStopMarker=/absolute/absent/path`.
// The environment variable takes precedence when both are supplied.
var TestRecoveryStopMarker string

func configureTestSeams(options *Options) {
	configureLegacyHealthFault(options)

	marker := os.Getenv(testRecoveryStopMarkerEnv)
	if strings.TrimSpace(marker) == "" {
		marker = TestRecoveryStopMarker
	}
	stopSeam.configureMarker(marker)
	realHealth := options.ConnectedHealth
	realReadiness := options.InitialReadiness
	options.ConnectedHealth = func(ctx context.Context, ref sessionapi.SessionRef, proxyAddr string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		stopSeam.observeMarker()
		if stopSeam.injectHealthFault(ref) {
			return errors.New("test recovery Stop health fault")
		}
		return realHealth(ctx, ref, proxyAddr)
	}
	options.InitialReadiness = func(ctx context.Context, ref sessionapi.SessionRef, proxyAddr string) error {
		if err := realReadiness(ctx, ref, proxyAddr); err != nil {
			if ctx.Err() != nil {
				stopSeam.cancelRecovery(ref)
			}
			return err
		}
		if !stopSeam.beginRecoveryHold(ref) {
			return nil
		}
		<-ctx.Done()
		err := ctx.Err()
		stopSeam.finishRecoveryHold(err)
		return err
	}
}

func configureLegacyHealthFault(options *Options) {
	raw := strings.TrimSpace(os.Getenv(testHealthFaultAfterEnv))
	if raw == "" {
		return
	}
	after, err := strconv.Atoi(raw)
	if err != nil || after < 0 {
		log.Debugf(category, "ignoring invalid %s value=%q", testHealthFaultAfterEnv, raw)
		return
	}

	var successful atomic.Int64
	options.ConnectedHealth = func(ctx context.Context, _ sessionapi.SessionRef, _ string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if successful.Add(1) > int64(after) {
			return fmt.Errorf("test health fault after %d successful checks", after)
		}
		// InitialReadiness remains the real check. The legacy build-local seam
		// starts at connected health and does not depend on live-probe timing.
		return nil
	}
	options.HealthInterval = time.Second
	options.HealthFailureThreshold = 1
}

// ArmTestRecoveryStop arms one process-wide fault for the next connected-health
// check after EnableTestRecoveryStop has enabled the seam. The check reports
// the injected failure, then a successful real InitialReadiness check for the
// next generation of the same session waits for its context to be cancelled by
// Stop. The function is absent from ordinary product builds and is intended
// only for build-local JNI or qualification controls.
func ArmTestRecoveryStop() bool { return stopSeam.arm() }

type recoveryStopPhase uint8

const (
	recoveryStopDisabled recoveryStopPhase = iota
	recoveryStopEnabled
	recoveryStopArmed
	recoveryStopFaultInjected
	recoveryStopHolding
)

type recoveryStopSeam struct {
	mu         sync.Mutex
	phase      recoveryStopPhase
	faultRef   sessionapi.SessionRef
	markerPath string
	markerSeen bool
}

func newRecoveryStopSeam() *recoveryStopSeam {
	return &recoveryStopSeam{}
}

var stopSeam = newRecoveryStopSeam()

func (s *recoveryStopSeam) configureMarker(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		s.mu.Lock()
		s.markerPath = ""
		s.markerSeen = false
		s.mu.Unlock()
		return
	}

	path, err := validateRecoveryStopMarker(raw)
	if err != nil {
		log.Debugf(category, "ignoring invalid %s setup: %v", testRecoveryStopMarkerEnv, err)
		path = ""
	}
	s.mu.Lock()
	s.markerPath = path
	s.markerSeen = false
	if path != "" {
		if s.phase == recoveryStopDisabled {
			s.phase = recoveryStopEnabled
		}
	}
	s.mu.Unlock()
}

func validateRecoveryStopMarker(raw string) (string, error) {
	if !filepath.IsAbs(raw) {
		return "", errors.New("marker path must be absolute")
	}
	path := filepath.Clean(raw)
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("marker parent is unavailable: %w", err)
	}
	if !parent.IsDir() {
		return "", errors.New("marker parent is not a directory")
	}
	if _, err := os.Lstat(path); err == nil {
		return "", errors.New("marker already exists")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect marker: %w", err)
	}
	return path, nil
}

// EnableTestRecoveryStop enables the tagged health monitor's short check
// interval for the current process. It has no effect until ArmTestRecoveryStop
// is called. Android's tagged JNI bridge calls Enable before Auto Start so the
// runtime may already have been constructed.
func EnableTestRecoveryStop() bool { return stopSeam.enable() }

func (s *recoveryStopSeam) enable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == recoveryStopDisabled {
		s.phase = recoveryStopEnabled
	}
	return s.phase == recoveryStopEnabled
}

func (s *recoveryStopSeam) arm() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != recoveryStopEnabled {
		return false
	}
	s.phase = recoveryStopArmed
	return true
}

func (s *recoveryStopSeam) observeMarker() {
	s.mu.Lock()
	path := s.markerPath
	s.mu.Unlock()
	if path == "" {
		return
	}
	s.observeMarkerAt(path)
}

func (s *recoveryStopSeam) observeMarkerAt(path string) {
	info, err := os.Lstat(path)
	present := err == nil && info.Mode().IsRegular()
	s.mu.Lock()
	defer s.mu.Unlock()
	if path != s.markerPath {
		return
	}
	if !present {
		if errors.Is(err, os.ErrNotExist) {
			s.markerSeen = false
		}
		return
	}
	if !s.markerSeen {
		s.markerSeen = true
		if s.phase == recoveryStopEnabled {
			s.phase = recoveryStopArmed
		}
	}
}

func (s *recoveryStopSeam) injectHealthFault(ref sessionapi.SessionRef) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != recoveryStopArmed {
		return false
	}
	s.phase = recoveryStopFaultInjected
	s.faultRef = ref
	return true
}

func (s *recoveryStopSeam) beginRecoveryHold(ref sessionapi.SessionRef) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != recoveryStopFaultInjected || ref.SessionID != s.faultRef.SessionID || ref.Generation <= s.faultRef.Generation {
		return false
	}
	s.phase = recoveryStopHolding
	return true
}

func (s *recoveryStopSeam) finishRecoveryHold(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != recoveryStopHolding {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// A readiness attempt timeout is retryable. Keep the fault pending so
		// the next successful real check holds again until Stop cancels the run.
		s.phase = recoveryStopFaultInjected
		return
	}
	s.phase = recoveryStopEnabled
	s.faultRef = sessionapi.SessionRef{}
}

func (s *recoveryStopSeam) cancelRecovery(ref sessionapi.SessionRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == recoveryStopFaultInjected && ref.SessionID == s.faultRef.SessionID && ref.Generation > s.faultRef.Generation {
		s.phase = recoveryStopEnabled
		s.faultRef = sessionapi.SessionRef{}
	}
}

func (s *recoveryStopSeam) monitorTiming(interval time.Duration, threshold int) (time.Duration, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != recoveryStopDisabled {
		return time.Second, 1
	}
	return interval, threshold
}

func testHealthMonitorTiming(interval time.Duration, threshold int) (checkInterval time.Duration, failureThreshold int) {
	return stopSeam.monitorTiming(interval, threshold)
}

// Test-only reset keeps unit tests independent. It is intentionally not part
// of the exported control surface.
func resetTestSeamsForTest() {
	stopSeam = newRecoveryStopSeam()
}
