//go:build darwin && !ios

package routing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"core/log"
	"golang.org/x/sys/unix"
)

// The token lives in /var/run, which macOS clears at boot together with the
// in-kernel pf state, so a reboot leaves nothing behind.
var macOSPFTokenPath = "/var/run/dobbyvpn/pf.token"

var macOSPFCommand = func(ctx context.Context, stdin string, args ...string) (string, string, error) {
	command := exec.CommandContext(ctx, "/sbin/pfctl", args...)
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	log.Debug(Category, "macOS pf command completed", map[string]any{
		"arguments": args, "stdout": stdout.String(), "stderr": stderr.String(), "error": errorText,
	})
	if err != nil {
		err = fmt.Errorf("pfctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), stderr.String(), err
}

// Read only the reference saved by the legacy daemon in its protected directory.
func legacyPFToken() (string, error) {
	dir, err := unix.Open(filepath.Dir(macOSPFTokenPath), unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer unix.Close(dir)
	var parent unix.Stat_t
	if err := unix.Fstat(dir, &parent); err != nil {
		return "", err
	}
	if int(parent.Uid) != os.Geteuid() || parent.Mode&0022 != 0 {
		return "", errors.New("untrusted legacy pf token directory")
	}
	fd, err := unix.Openat(dir, filepath.Base(macOSPFTokenPath), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), macOSPFTokenPath)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	st := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || int(st.Uid) != os.Geteuid() || st.Nlink != 1 || info.Mode().Perm()&0022 != 0 || info.Size() > 32 {
		return "", errors.New("untrusted legacy pf token file")
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	number, err := strconv.ParseUint(token, 10, 64)
	if err != nil || number == 0 {
		return "", errors.New("invalid legacy pf token")
	}
	return token, nil
}

func legacyPFReference(ctx context.Context) (token string, held bool, err error) {
	token, err = legacyPFToken()
	if err != nil || token == "" {
		return token, false, err
	}
	refs, _, err := macOSPFCommand(ctx, "", "-s", "References")
	if err != nil {
		return token, false, err
	}
	held, err = pfHasReference(refs, token)
	return token, held, err
}

func LegacyMacOSGuardActive(ctx context.Context) (bool, error) {
	_, held, err := legacyPFReference(ctx)
	return held, err
}

// ReleaseLegacyMacOSGuard runs after the new packet path owns protection,
// or when Stop/uninstall or an inactive installation releases the old policy.
func ReleaseLegacyMacOSGuard(ctx context.Context) error {
	token, held, err := legacyPFReference(ctx)
	if err != nil || token == "" {
		return err
	}
	if held {
		for _, kind := range []string{"rules", "Tables"} {
			if _, _, err := macOSPFCommand(ctx, "", "-a", MacOSPFAnchor, "-F", kind); err != nil {
				return err
			}
		}
		if _, _, err := macOSPFCommand(ctx, "", "-X", token); err != nil {
			return err
		}
	}
	log.Info(Category, "retiring legacy macOS kill switch token", map[string]any{"anchor": MacOSPFAnchor, "reference_held": held})
	if err := os.Remove(macOSPFTokenPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
