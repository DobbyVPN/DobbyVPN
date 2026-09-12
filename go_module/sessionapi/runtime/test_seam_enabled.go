//go:build dobbyvpn_test_seams

package runtime

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go_module/log"
	v2 "go_module/sessionapi/v2"
)

// This product-owned seam is compiled only into a build-local qualification
// binary. Release builds do not read this variable or contain this injector.
const testHealthFaultAfterEnv = "DOBBYVPN_TEST_HEALTH_FAULT_AFTER_SUCCESSFUL_CHECKS"

func configureTestSeams(options *Options) {
	raw := strings.TrimSpace(os.Getenv(testHealthFaultAfterEnv))
	if raw == "" {
		return
	}
	after, err := strconv.Atoi(raw)
	if err != nil || after < 0 {
		log.Debugf(category, "ignoring invalid %s value=%q", testHealthFaultAfterEnv, raw)
		return
	}

	var successful atomic.Int64
	options.ConnectedHealth = func(ctx context.Context, _ v2.SessionRef) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if successful.Add(1) > int64(after) {
			return fmt.Errorf("test health fault after %d successful checks", after)
		}
		// InitialReadiness remains the real check. The build-local seam starts
		// at connected health, so qualification is deterministic and does not
		// depend on live-probe timing before the injected failure.
		return nil
	}
	options.HealthInterval = time.Second
	options.HealthFailureThreshold = 1
}
