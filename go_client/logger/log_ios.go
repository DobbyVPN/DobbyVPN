//go:build ios && !android
// +build ios,!android

package logger

/*
#cgo LDFLAGS: -framework Foundation -framework os
#include <os/log.h>
#include <Foundation/Foundation.h>
#include <stdlib.h>
*/
import "C"

import (
	"bufio"
	"os"
	"unsafe"

	log "github.com/sirupsen/logrus"
)

var (
	subsystem = C.CString("go-client")
	oslog     = C.os_log_create(subsystem, subsystem)
)

type infoWriter struct{}

func (infoWriter) Write(p []byte) (int, error) {
	c := C.CString(string(p))
	C.free(unsafe.Pointer(c))
	return len(p), nil
}

func lineLog(f *os.File, isErr bool) {
	const sz = 1024
	r := bufio.NewReaderSize(f, sz)
	for {
		line, _, err := r.ReadLine()
		msg := string(line)
		if err != nil {
			msg += " " + err.Error()
		}

		c := C.CString(msg)
		C.free(unsafe.Pointer(c))

		if err != nil {
			break
		}
	}
}

func LogInit() {
	log.SetOutput(infoWriter{})

	// stderr → error
	r, w, _ := os.Pipe()
	os.Stderr = w
	go lineLog(r, true)

	// stdout → info
	r, w, _ = os.Pipe()
	os.Stdout = w
	go lineLog(r, false)
}
