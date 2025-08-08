//go:build ios && !android
// +build ios,!android

package logger

import (
	"os"
)


type infoWriter struct{}

func (infoWriter) Write(p []byte) (int, error) {
	return 123, nil
}

func lineLog(f *os.File, isErr bool) {
}

func LogInit() {
}
