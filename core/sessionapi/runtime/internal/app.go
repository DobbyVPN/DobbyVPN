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
	"core/sessionapi"
	"core/tunnel"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
)

// namedRelease keeps the stage name of one cleanup entry so the timeline can
// report which release blocked without guessing from a function pointer.
type namedRelease struct {
	name string
	fn   func(context.Context) error
}

type App struct {
	NetworkRef     sessionapi.SessionRef
	cleanupMu      sync.Mutex
	cleanup        []namedRelease
	cleanupErr     error
	cleanupContext context.Context

	Engine          *tunnel.Engine
	UplinkInterface string
	DNSCache        *dnscache.Cache
	BypassPolicy    *tunnel.BypassPolicy
	RoutingConfig   *RoutingConfig
}

type RoutingConfig struct {
	TunDeviceName        string
	TunDeviceIP          string
	TunDeviceMTU         int
	TunGatewayCIDR       string
	RoutingTableID       int
	RoutingTablePriority int
	DNSServers           []string
}

// The run goroutine registers one ordered cleanup transaction before acquiring
// resources. After it returns, Disconnect may retry only its remaining entries.
func (app *App) setCleanup(releases ...namedRelease) {
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
		release := app.cleanup[0]
		ended := log.StageBegin("cleanup", release.name, "")
		err := release.fn(ctx)
		ended(internalStageResult(err))
		if err != nil {
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

// SetCleanupContext supplies the current network owner's deadline before the
// run goroutine is canceled. Attempt contexts have a shorter, independent life.
func (app *App) SetCleanupContext(ctx context.Context) {
	app.cleanupMu.Lock()
	defer app.cleanupMu.Unlock()
	app.cleanupContext = ctx
}

func (app *App) finishCleanup(ctx context.Context, runErr error) error {
	app.cleanupMu.Lock()
	cleanupCtx := app.cleanupContext
	app.cleanupMu.Unlock()
	if cleanupCtx == nil {
		cleanupCtx = sessionapi.CleanupContext(ctx)
	}
	if err := app.Close(cleanupCtx); err != nil {
		runErr = errors.Join(runErr, err)
		log.Errorf(Category, "native cleanup pending: %v", err)
	} else {
		log.Infof(Category, "native cleanup_complete=true")
	}
	return runErr
}

// internalStageResult names one finished cleanup release.
func internalStageResult(err error) string {
	if err != nil {
		return "failed"
	}
	return "ok"
}
