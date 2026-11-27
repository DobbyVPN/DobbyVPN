package sessionapi

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const acceptedFileName = "accepted-configuration.v1.json"

// Work through an open directory handle, including the final rename. A
// privileged service must not redirect a read or write through a peer's symlink.
func (s FileSourceStore) acceptedRoot(create bool) (opened *os.Root, resultErr error) {
	dir := filepath.Dir(s.Path)
	if s.ProtectedDirectory != "" {
		dir = s.ProtectedDirectory
	}
	if err := validateNoSymlinkAncestors(dir); err != nil {
		return nil, err
	}
	owner := s.DirectoryOwner
	if !create {
		if _, err := os.Lstat(dir); err != nil {
			return nil, err
		}
	}
	if err := prepareSourceDirectory(dir, owner); err != nil {
		return nil, err
	}
	if err := secureAcceptedDirectory(dir, s.ProtectedDirectory != ""); err != nil {
		return nil, err
	}
	if owner == nil {
		return os.OpenRoot(dir)
	}
	home, err := os.OpenRoot(owner.Root)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := home.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
			if opened != nil {
				resultErr = errors.Join(resultErr, opened.Close())
				opened = nil
			}
		}
	}()
	relative, err := filepath.Rel(owner.Root, dir)
	if err != nil {
		return nil, err
	}
	return home.OpenRoot(relative)
}

func (s FileSourceStore) LoadAccepted(ctx context.Context) (data []byte, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := s.acceptedRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	before, err := root.Lstat(acceptedFileName)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > maxAcceptedRecord {
		return nil, errors.New("invalid accepted configuration file")
	}
	file, err := root.Open(acceptedFileName)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, errors.New("accepted configuration file changed while opening")
	}
	if accessErr := s.verifyAcceptedAccess(file, opened); accessErr != nil {
		return nil, accessErr
	}
	data, err = io.ReadAll(io.LimitReader(file, maxAcceptedRecord+1))
	if len(data) > maxAcceptedRecord {
		return nil, errors.New("accepted configuration file is too large")
	}
	return data, err
}

func (s FileSourceStore) SaveAccepted(ctx context.Context, raw []byte) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > maxAcceptedRecord {
		return errors.New("invalid accepted configuration record size")
	}
	root, err := s.acceptedRoot(true)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if info, statErr := root.Lstat(acceptedFileName); statErr == nil && !info.Mode().IsRegular() {
		return errors.New("accepted configuration destination is not a regular file")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	tmp := ".accepted-" + rand.Text()
	file, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := root.Remove(tmp); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, removeErr)
		}
	}()
	if owner := s.DirectoryOwner; owner != nil {
		err = file.Chown(owner.UID, owner.GID)
	}
	if err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	if writeErr := errors.Join(err, file.Close()); writeErr != nil {
		return writeErr
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if err = root.Rename(tmp, acceptedFileName); err != nil {
		return fmt.Errorf("replace accepted configuration: %w", err)
	}
	return syncAcceptedDirectory(root)
}

func (s FileSourceStore) ClearAccepted(ctx context.Context) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := s.acceptedRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if err = root.Remove(acceptedFileName); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return syncAcceptedDirectory(root)
}
