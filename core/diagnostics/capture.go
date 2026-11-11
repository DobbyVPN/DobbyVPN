package diagnostics

import (
	"bytes"
	"runtime/pprof"
)

// A hang capture is one bounded all-goroutine snapshot. It is deliberately
// independent of the logger, the session cleanup transaction and the engine's
// stop locks: a blocked operation must still be captured and exported while it
// is stuck. The capture is returned as bytes and written as one ordinary log
// record, so the retained file budget stays the four log files the logging
// contract allows instead of adding a separate dump set.
const (
	// HangCaptureMaxBytes caps one captured stack set before it is written.
	HangCaptureMaxBytes = 1 << 20
)

// hangCaptureMaxBytes is a package variable so in-package tests can force
// truncation without generating a real megabyte of stacks.
var hangCaptureMaxBytes int64 = HangCaptureMaxBytes

// CaptureGoroutines collects one all-goroutine stack set once. Truncation and
// profile failures are reported to the caller; a profile that produced no
// bytes at all is an error.
func CaptureGoroutines() (stacks []byte, truncated bool, err error) {
	var buffer bytes.Buffer
	// Debug level two is the same all-goroutine form the slow-stop diagnostics
	// have always used, so readers find the same frames.
	profileErr := pprof.Lookup("goroutine").WriteTo(&buffer, 2)
	data := buffer.Bytes()
	if profileErr != nil && len(data) == 0 {
		return nil, false, profileErr
	}
	truncated = profileErr != nil || int64(len(data)) > hangCaptureMaxBytes
	if int64(len(data)) > hangCaptureMaxBytes {
		data = data[:hangCaptureMaxBytes]
	}
	return data, truncated, nil
}

// CaptureResult states one capture outcome in a stage record. A failure keeps
// its text so an export can tell a refused capture from an absent one.
func CaptureResult(truncated bool, captureErr error) string {
	if captureErr != nil {
		return "failed: " + captureErr.Error()
	}
	if truncated {
		return "truncated"
	}
	return "ok"
}
