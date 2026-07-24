package vpnmanager

import (
	"sync"

	"go_module/log"
)

// LastError stores the most recent export-layer error for int32-returning FFI APIs.
type LastError struct {
	mu       sync.Mutex
	message  string
	category string
}

// NewLastError creates a thread-safe last-error store with optional log category.
func NewLastError(category string) *LastError {
	return &LastError{category: category}
}

func (e *LastError) Get() string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.message
}

func (e *LastError) Clear() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.message = ""
}

func (e *LastError) Set(message string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.message = message
	if message != "" && e.category != "" {
		log.Debugf(e.category, "Error set: %s", message)
	}
}
