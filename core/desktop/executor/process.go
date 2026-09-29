//go:build !(android || ios)

package executor

import (
	"fmt"
	"sync"

	"core/desktop/controlplane"
	"core/sessionapi"
	"core/sessionapi/mobilebinding"
	"core/sessionapi/runtimebridge"
)

var (
	processOnce    sync.Once
	processBinding *mobilebinding.Binding
)

func desktopProcessBinding() *mobilebinding.Binding {
	processOnce.Do(func() {
		sourceStore, err := controlplane.ConnectionSourceStore()
		if err != nil {
			panic(fmt.Sprintf("failed to locate desktop connection URL storage: %v", err))
		}
		manager := sessionapi.NewManager(sessionapi.ManagerOptions{
			Runtime:     runtimebridge.New(nil),
			SourceStore: sourceStore,
		})
		processBinding = mobilebinding.NewForDesktop(manager)
	})
	return processBinding
}
