//go:build darwin
// +build darwin

package routing

import (
	"fmt"
	"os/exec"
	log "github.com/sirupsen/logrus"
	"strings"
)

func ExecuteAsAdmin(commands []string) (string, error) {
	script := fmt.Sprintf(`do shell script "%s" with administrator privileges`,
		strings.Join(commands, "; "))

	log.Infof("Routing: running AppleScript: %s", script)
    cmd := exec.Command("osascript", "-e", script)
    output, err := cmd.CombinedOutput()
    log.Infof("Routing: AppleScript finished, output=%s, err=%v", output, err)

	if err != nil {
		return string(output), fmt.Errorf("osascript execution failed: %w, output: %s", err, output)
	}
	log.Infof("Routing: executed with admin privileges: %s", script)
	return string(output), nil
}

func StartRouting(proxyIP string, gatewayIP string, tunName string) error {
	commands := []string{
	    fmt.Sprintf("route add -net %s/32 %s", "85.9.223.19", gatewayIP),
		fmt.Sprintf("ifconfig %s inet 169.254.19.0 169.254.19.0 netmask 255.255.255.0", tunName),
		fmt.Sprintf("route delete default"),
		fmt.Sprintf("route add default -interface %s", tunName),
		fmt.Sprintf("route add -net %s/32 %s", proxyIP, gatewayIP),
	}

	if _, err := ExecuteAsAdmin(commands); err != nil {
		log.Warnf("failed to execute StartRouting: %v", err)
		return err
	}
	return nil
}

func StopRouting(proxyIP string, gatewayIP string) error {
	commands := []string{
	    fmt.Sprintf("route delete -net %s/32 %s", "85.9.223.19", gatewayIP),
		fmt.Sprintf("route delete -net %s/32 %s", proxyIP, gatewayIP),
		fmt.Sprintf("route delete default"),
		fmt.Sprintf("route add default %s", gatewayIP),
	}

	if _, err := ExecuteAsAdmin(commands); err != nil {
		log.Warnf("failed to execute StopRouting: %v", err)
		return err
	}
	return nil
}
