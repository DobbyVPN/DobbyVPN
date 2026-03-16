package api

import (
	"go_client/drivers"
)

func AddTapDevice(appDir string) {
	drivers.AddTapDevice(appDir)
}
