//go:build linux && !(android || ios)

package internal

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDuplicateTunFDHasIndependentLifetime(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	engineFD, err := duplicateTunFD(int(reader.Fd()))
	if err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(engineFD) })

	if err := reader.Close(); err != nil {
		t.Fatalf("close original descriptor: %v", err)
	}
	if _, err := writer.Write([]byte{'x'}); err != nil {
		t.Fatalf("write after closing original descriptor: %v", err)
	}
	buffer := make([]byte, 1)
	if count, err := unix.Read(engineFD, buffer); err != nil || count != 1 || buffer[0] != 'x' {
		t.Fatalf("duplicated descriptor read count=%d value=%q err=%v", count, buffer, err)
	}
}
