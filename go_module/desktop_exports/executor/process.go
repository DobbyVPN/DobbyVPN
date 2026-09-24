//go:build !(android || ios)

package executor

import (
	"fmt"
	"sync"

	"go_module/desktop_exports/controlplane"
	"go_module/sessionapi"
	"go_module/sessionapi/mobilebinding"
	"go_module/sessionapi/runtimebridge"
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
