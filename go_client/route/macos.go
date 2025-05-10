//go:build darwin

package route

import (
	"bytes"
	"fmt"
	"github.com/jackpal/gateway"
	"net"
	"os/exec"
)

func InstallBypassRoutes(cidrs []*net.IPNet, _ int) error {
	if len(cidrs) == 0 {
		return nil
	}

	gw, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("discover gateway: %w", err)
	}
	gwIP := gw.String()

	for _, n := range cidrs {
		cmd := exec.Command("route", "add", "-net", n.String(), gwIP)
		if out, err := cmd.CombinedOutput(); err != nil {
			if isExistsDarwin(out) {
				c := exec.Command("route", "change", "-net", n.String(), gwIP)
				if out2, err2 := c.CombinedOutput(); err2 != nil && !isExistsDarwin(out2) {
					return fmt.Errorf("route change %s: %v (%s)", n, err2, out2)
				}
				continue
			}
			return fmt.Errorf("route add %s: %v (%s)", n, err, out)
		}
	}
	return nil
}

func isExistsDarwin(out []byte) bool {
	return bytes.Contains(out, []byte("File exists"))
}
