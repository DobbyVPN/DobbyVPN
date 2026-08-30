//go:build darwin
// +build darwin

package routing

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"go_module/log"
)

func ExecuteCommand(command string) (string, error) {
	log.Debugf(Category, "[Exec] Running route command: %s", log.MaskStr(command))

	args := strings.Fields(command)
	if len(args) == 0 {
		return "", fmt.Errorf("empty command")
	}
	if args[0] != "route" {
		return "", fmt.Errorf("unsupported routing command: %s", args[0])
	}

	cmd := exec.CommandContext(context.Background(), "route", args[1:]...) // #nosec G204 command is restricted to the route binary above.
	output, err := cmd.CombinedOutput()
	outStr := string(output)

	if err != nil {
		log.Debugf(Category, "[Exec] ERROR: command failed: %v | output=%s", err, outStr)
		return outStr, fmt.Errorf("command execution failed: %w, output: %s", err, outStr)
	}

	log.Debugf(Category, "[Exec] OK: output=%s", outStr)
	return outStr, nil
}

func isLoopbackIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

var ipv6DefaultSubnets = []string{ipv6LowerHalf, ipv6UpperHalf}
