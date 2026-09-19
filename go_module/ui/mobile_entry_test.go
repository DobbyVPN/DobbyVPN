//go:build android || ios

package ui

import "testing"

func TestMobileProductionEntryPointInjectsLogExporter(t *testing.T) {
	if NewMobileLogExporter() == nil {
		t.Fatal("mobile production entrypoint has no log exporter")
	}
}
