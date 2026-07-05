package protected_dialer

import "runtime"

func XraySockopt(routingTableID int, uplinkIface string) map[string]interface{} {
	switch runtime.GOOS {
	case "linux", "android":
		return map[string]interface{}{"mark": routingTableID}
	case "windows", "darwin":
		if uplinkIface != "" {
			return map[string]interface{}{"interface": uplinkIface}
		}
	}
	return nil
}
