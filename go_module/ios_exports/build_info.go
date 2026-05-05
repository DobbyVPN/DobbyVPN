package cloak_outline

import (
	"go_module/log"
	"runtime"
	"runtime/debug"
	"strings"
)

const nativeFeatureBuild = "ios-native/2026-05-05.3 features=disableNonDNSUDP,outlineDialCounters,nativeBuildInfo,protectionDiagnostics,routedUDPHealth"

func NativeBuildInfo() string {
	parts := []string{nativeFeatureBuild, "go=" + runtime.Version()}

	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			parts = append(parts, "mainVersion="+info.Main.Version)
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if setting.Value != "" {
					parts = append(parts, "vcsRevision="+setting.Value)
				}
			case "vcs.modified":
				if setting.Value != "" {
					parts = append(parts, "vcsModified="+setting.Value)
				}
			}
		}
	}

	return strings.Join(parts, " ")
}

func logNativeBuildInfo(context string) {
	log.Infof("%s nativeBuildInfo=%s", context, NativeBuildInfo())
}
