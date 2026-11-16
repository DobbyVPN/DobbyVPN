package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LegacySource identifies a former diagnostic stream and its producer.
// Both its previous and current generations are incorporated.
type LegacySource struct {
	Path  string
	Label string
}

// MigrateLegacy merges former streams into the bounded destination history.
// Call before any destination writers or stderr capture are started.
func MigrateLegacy(path string, sources []LegacySource) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(absolute), 0o700); mkdirErr != nil {
		return mkdirErr
	}
	for i := range sources {
		sources[i].Path, err = filepath.Abs(sources[i].Path)
		if err != nil {
			return err
		}
		if sources[i].Path == absolute {
			return errors.New("legacy source equals destination")
		}
	}
	return withFileLock(absolute, func() error { return migrateLegacyLocked(absolute, sources, Threshold) })
}

// readLegacyMarker bounds the committed metadata and rejects links or
// nonregular files before reading; a broken FIFO cannot stall startup.
func readLegacyMarker(path string) (data []byte, resultErr error) {
	file, err := openStartupMarker(path)
	if err != nil {
		return nil, fmt.Errorf("open migration marker: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	if _, statErr := regularFile(file); statErr != nil {
		return nil, statErr
	}
	const maxMarker = 1024 * 1024
	data, err = io.ReadAll(io.LimitReader(file, maxMarker+1))
	if err != nil {
		return nil, fmt.Errorf("read migration marker: %w", err)
	}
	if len(data) > maxMarker {
		return nil, errors.New("migration marker exceeds size limit")
	}
	return data, nil
}

type legacyCommit struct {
	Sources  []LegacySource
	Previous bool
}

func migrateLegacyLocked(path string, sources []LegacySource, limit int64) error {
	return migrateLegacyTransaction(path, sources, limit, false)
}

func migrateLegacyTransaction(path string, sources []LegacySource, limit int64, force bool) error {
	stage, marker := path+".legacy-stage", path+".legacy-commit"
	data, readErr := readLegacyMarker(marker)
	if readErr == nil {
		return recoverLegacyCommit(path, stage, marker, data, sources, limit, force)
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if removeErr := removeIfPresent(stage); removeErr != nil {
		return removeErr
	}
	if removeErr := removeIfPresent(stage + PreviousSuffix); removeErr != nil {
		return removeErr
	}
	found, findErr := hasLegacySources(sources, force)
	if findErr != nil {
		return findErr
	}
	if !found {
		return nil
	}
	if err := stageLegacyStreams(path, stage, sources, limit, force); err != nil {
		return err
	}
	commit, err := syncLegacyStages(path, stage, sources)
	if err != nil {
		return err
	}
	if publishErr := publishLegacyMarker(path, marker, commit); publishErr != nil {
		return publishErr
	}
	return finishLegacyCommit(path, stage, marker, commit)
}

func recoverLegacyCommit(path, stage, marker string, data []byte, sources []LegacySource, limit int64, force bool) error {
	var commit legacyCommit
	if err := json.Unmarshal(data, &commit); err != nil {
		return err
	}
	if commit.Sources == nil && sources != nil {
		if err := finishLegacyCommit(path, stage, marker, commit); err != nil {
			return err
		}
		return migrateLegacyTransaction(path, sources, limit, force)
	}
	want, _ := json.Marshal(sources)
	got, _ := json.Marshal(commit.Sources)
	if !bytes.Equal(want, got) {
		return errors.New("legacy migration sources changed during recovery")
	}
	return finishLegacyCommit(path, stage, marker, commit)
}

func hasLegacySources(sources []LegacySource, found bool) (bool, error) {
	for _, source := range sources {
		for _, suffix := range []string{PreviousSuffix, ""} {
			if _, err := os.Stat(source.Path + suffix); err == nil {
				found = true
			} else if !errors.Is(err, os.ErrNotExist) {
				return false, err
			}
		}
	}
	return found, nil
}

func stageLegacyStreams(path, stage string, sources []LegacySource, limit int64, force bool) error {
	var size int64
	start := true
	streams := append([]LegacySource{{Path: path, Label: "existing"}}, sources...)
	for _, source := range streams {
		for _, suffix := range []string{PreviousSuffix, ""} {
			input, openErr := openLegacyInput(source.Path + suffix)
			if errors.Is(openErr, os.ErrNotExist) {
				continue
			}
			if openErr != nil {
				return openErr
			}
			if _, statErr := regularFile(input); statErr != nil {
				return errors.Join(statErr, input.Close())
			}
			header, encodeErr := json.Marshal(map[string]string{"event": "diagnostics.legacy", "source": source.Label, "generation": suffix})
			if encodeErr == nil {
				header = append(header, '\n')
				if force && len(sources) == 0 {
					header = nil
				}
				if !start && len(header) != 0 {
					header = append([]byte{'\n'}, header...)
					start = true
				}
				encodeErr = streamMigrationLabeled(input, stage, limit, &size, &start, header)
			}
			if closeErr := errors.Join(encodeErr, input.Close()); closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}

func syncLegacyStages(path, stage string, sources []LegacySource) (legacyCommit, error) {
	commit := legacyCommit{Sources: sources}
	for _, suffix := range []string{PreviousSuffix, ""} {
		file, err := openExistingAppend(stage + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return commit, err
		}
		if suffix == PreviousSuffix {
			commit.Previous = true
		}
		err = preserveMigrationPermissions(file, path)
		if joinedErr := errors.Join(err, file.Sync(), file.Close()); joinedErr != nil {
			return commit, joinedErr
		}
	}
	return commit, nil
}

func publishLegacyMarker(path, marker string, commit legacyCommit) error {
	data, err := json.Marshal(commit)
	if err != nil {
		return err
	}
	temp := marker + ".tmp"
	if removeErr := removeIfPresent(temp); removeErr != nil {
		return removeErr
	}
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if joinedErr := errors.Join(err, file.Sync(), file.Close()); joinedErr != nil {
		return joinedErr
	}
	if publishErr := publishFile(temp, marker); publishErr != nil {
		return publishErr
	}
	return syncDirectory(filepath.Dir(path))
}

func finishLegacyCommit(path, stage, marker string, commit legacyCommit) error {
	for _, suffix := range []string{PreviousSuffix, ""} {
		if suffix == PreviousSuffix && !commit.Previous {
			if err := removeIfPresent(path + suffix); err != nil {
				return err
			}
			continue
		}
		if _, err := os.Stat(stage + suffix); err == nil {
			if publishErr := publishFile(stage+suffix, path+suffix); publishErr != nil {
				return fmt.Errorf("publish migrated diagnostics: %w", publishErr)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	}
	if err := validatePublishedLegacy(path, commit.Previous); err != nil {
		return err
	}
	for _, source := range commit.Sources {
		for _, suffix := range []string{PreviousSuffix, ""} {
			if err := removeIfPresent(source.Path + suffix); err != nil {
				return err
			}
		}
	}
	for _, source := range commit.Sources {
		if err := syncDirectory(filepath.Dir(source.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err := removeIfPresent(marker); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// A missing stage is published only if its regular destination exists.
// Keep all legacy sources when a committed generation is missing or unsafe.
func validatePublishedLegacy(path string, previous bool) error {
	for _, suffix := range []string{"", PreviousSuffix} {
		if suffix == PreviousSuffix && !previous {
			continue
		}
		file, err := openStartupMarker(path + suffix)
		if err != nil {
			return fmt.Errorf("open published diagnostics %s: %w", path+suffix, err)
		}
		_, statErr := regularFile(file)
		if validationErr := errors.Join(statErr, file.Close()); validationErr != nil {
			return fmt.Errorf("validate published diagnostics %s: %w", path+suffix, validationErr)
		}
	}
	return nil
}
