//go:build !windows

package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func tryUploadLock(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func uploadLockContended(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
func releaseUploadLock(file *os.File) error                    { return unix.Flock(int(file.Fd()), unix.LOCK_UN) }
func replaceUploadPreference(source, destination string) error { return os.Rename(source, destination) }
