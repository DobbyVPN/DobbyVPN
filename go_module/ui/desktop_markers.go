//go:build !(android || ios)

package ui

func MarkStartup()    {}
func markUIAttached() {}

func newDefaultDiagnosticStore() DiagnosticStore {
	store, err := NewFileDiagnosticStore()
	if err != nil {
		return unavailableDiagnosticStore{err: err}
	}
	return store
}
