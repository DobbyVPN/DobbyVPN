// Copyright 2023 The Outline Authors
// Modified for DobbyVPN; modifications Copyright 2026 Alexander Potemkin.
// See the project LICENSE for terms governing the DobbyVPN modifications.
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
	"context"
	"core/dnscache"
	"core/log"
	"core/protocol"
	"core/sessionapi"
	"core/tunnel"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
)

type App struct {
	cleanupMu  sync.Mutex
	cleanup    []func(context.Context) error
	cleanupErr error

	ProtocolDevice protocol.ProtocolDevice
	DNSCache       *dnscache.Cache
	BypassPolicy   *tunnel.BypassPolicy
	RoutingConfig  *RoutingConfig
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

// The run goroutine registers one ordered cleanup transaction before acquiring
// resources. After it returns, Disconnect may retry only its remaining entries.
func (app *App) setCleanup(releases ...func(context.Context) error) {
	app.cleanupMu.Lock()
	defer app.cleanupMu.Unlock()
	app.cleanup = releases
}

func (app *App) Close(ctx context.Context) (resultErr error) {
	app.cleanupMu.Lock()
	defer app.cleanupMu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			app.cleanupErr = fmt.Errorf("native cleanup panic: %v\n%s", recovered, debug.Stack())
			resultErr = &sessionapi.CleanupFailure{Err: app.cleanupErr}
		}
	}()
	for len(app.cleanup) > 0 {
		if err := app.cleanup[0](ctx); err != nil {
			app.cleanupErr = err
			return &sessionapi.CleanupFailure{Err: err}
		}
		app.cleanup = app.cleanup[1:]
	}
	app.cleanupErr = nil
	return nil
}

func (app *App) CleanupError() error {
	app.cleanupMu.Lock()
	defer app.cleanupMu.Unlock()
	return app.cleanupErr
}

func (app *App) finishCleanup(ctx context.Context, runErr *error) {
	if err := app.Close(sessionapi.CleanupContext(ctx)); err != nil {
		*runErr = errors.Join(*runErr, err)
		log.Errorf(Category, "native cleanup pending: %v", err)
	} else {
		log.Infof(Category, "native cleanup_complete=true")
	}
}
