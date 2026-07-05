// Copyright 2023 The Outline Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package internal

import (
	"sync"

	"go_module/core/pkg"
)

type App struct {
	ProtocolDevice pkg.ProtocolDevice
	RoutingConfig  *RoutingConfig

	mu            sync.Mutex
	currentDevice pkg.ProtocolDevice
	gatewayIP     string
	uplinkIface   string
	tunIface      string
	serverIP      string
	running       bool
}

type RoutingConfig struct {
	TunDeviceName        string
	TunDeviceIP          string
	TunDeviceMTU         int
	TunGatewayCIDR       string
	RoutingTableID       int
	RoutingTablePriority int
	DNSServerIP          string
}
