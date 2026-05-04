package cloak_outline

import (
	"fmt"
	"go_module/netcheck"
	"strings"
)

func NetCheck(configPath string) string {
	err := netcheck.NetCheck(strings.Clone(configPath))
	if err != nil {
		return fmt.Sprintf("NetCheck error: %v", err)
	} else {
		return ""
	}
}

func CancelNetCheck() {
	netcheck.CancelNetCheck()
}
