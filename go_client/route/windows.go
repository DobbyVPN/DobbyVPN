//go:build windows

package route

import (
	"bytes"
	"fmt"
	"github.com/jackpal/gateway"
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
		mask := net.IP(n.Mask).String()
		dest := n.IP.String()

		cmd := exec.Command("route", "add", dest, "mask", mask, gwIP, "-p")
		if out, err := cmd.CombinedOutput(); err != nil {
			if isExistsWin(out) {
				c := exec.Command("route", "change", dest, "mask", mask, gwIP)
				if out2, err2 := c.CombinedOutput(); err2 != nil && !isExistsWin(out2) {
					return fmt.Errorf("route change %s: %v (%s)", n, err2, out2)
				}
				continue
			}
			return fmt.Errorf("route add %s: %v (%s)", n, err, out)
		}
	}
	return nil
}

func isExistsWin(out []byte) bool {
	return bytes.Contains(out, []byte("already exists"))
}
