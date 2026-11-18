//go:build !windows

package sessionapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func prepareSourceDirectory(dir string, owner *SourceDirectoryOwner) (resultErr error) {
	if owner == nil {
		return os.MkdirAll(dir, 0o700)
	}
	if owner.UID < 0 || owner.GID < 0 || !filepath.IsAbs(owner.Root) {
		return errors.New("saved configuration directory owner is invalid")
	}
	relative, err := filepath.Rel(owner.Root, dir)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return errors.New("saved configuration directory is outside the peer home")
	}
	root, err := os.OpenRoot(owner.Root)
	if err != nil {
		return err
	}
	defer func() {
		if err := root.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close saved configuration directory root: %w", err))
		}
	}()
	current := ""
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err := preparePeerSourceDirectory(root, current, owner); err != nil {
			return err
		}
	}
	return nil
}

func preparePeerSourceDirectory(root *os.Root, current string, owner *SourceDirectoryOwner) error {
	if err := root.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := root.Lstat(current)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("saved configuration directory is not a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && int(stat.Uid) != owner.UID) {
		return fmt.Errorf("saved configuration directory %s belongs to another account", current)
	}
	// A privileged daemon may create or repair its peer's private state
	// directory. The capability confines ownership changes to that home;
	// no symlinked directory or unrelated ancestor is adopted.
	if err := root.Chown(current, owner.UID, owner.GID); err != nil {
		return err
	}
	if err := root.Chmod(current, 0o700); err != nil {
		return err
	}
	return nil
}
