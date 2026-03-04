//go:build !e2e

package e2e_test

import "testing"

func TestE2ERequiresBuildTag(t *testing.T) {
	t.Skip("e2e tests are disabled by default; run with -tags=e2e")
}
