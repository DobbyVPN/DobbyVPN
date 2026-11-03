package diagnostics

import (
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
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return err
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
	if _, err := regularFile(file); err != nil {
		return nil, err
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
	data, err := readLegacyMarker(marker)
	if err == nil {
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
		if string(want) != string(got) {
			return errors.New("legacy migration sources changed during recovery")
		}
		return finishLegacyCommit(path, stage, marker, commit)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := removeIfPresent(stage); err != nil {
		return err
	}
	if err := removeIfPresent(stage + PreviousSuffix); err != nil {
		return err
	}
	found := force
	for _, source := range sources {
		for _, suffix := range []string{PreviousSuffix, ""} {
			if _, err := os.Stat(source.Path + suffix); err == nil {
				found = true
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if !found {
		oversized, _, err := historyNeedsMigration(path, limit)
		if err != nil || !oversized {
			return err
		}
		return migrateLegacyTransaction(path, nil, limit, true)
	}
	var size int64
	start := true
	streams := append([]LegacySource{{Path: path, Label: "existing"}}, sources...)
	for _, source := range streams {
		for _, suffix := range []string{PreviousSuffix, ""} {
			input, err := openLegacyInput(source.Path + suffix)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if _, err := regularFile(input); err != nil {
				return errors.Join(err, input.Close())
			}
			header, err := json.Marshal(map[string]string{"event": "diagnostics.legacy", "source": source.Label, "generation": suffix})
			if err == nil {
				header = append(header, '\n')
				if force && len(sources) == 0 {
					header = nil
				}
				if !start && len(header) != 0 {
					header = append([]byte{'\n'}, header...)
					start = true
				}
				err = streamMigrationLabeled(input, stage, limit, &size, &start, header)
			}
			if err = errors.Join(err, input.Close()); err != nil {
				return err
			}
		}
	}
	commit := legacyCommit{Sources: sources}
	for _, suffix := range []string{PreviousSuffix, ""} {
		file, err := openExistingAppend(stage + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if suffix == PreviousSuffix {
			commit.Previous = true
		}
		err = preserveMigrationPermissions(file, path)
		err = errors.Join(err, file.Sync(), file.Close())
		if err != nil {
			return err
		}
	}
	data, err = json.Marshal(commit)
	if err != nil {
		return err
	}
	temp := marker + ".tmp"
	if err := removeIfPresent(temp); err != nil {
		return err
	}
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err := publishFile(temp, marker); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	return finishLegacyCommit(path, stage, marker, commit)
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
			if err := publishFile(stage+suffix, path+suffix); err != nil {
				return fmt.Errorf("publish migrated diagnostics: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	}
	// A missing staged file means it was already published only if the
	// corresponding regular destination actually exists. Never delete legacy
	// sources while either committed generation is missing or unsafe.
	for _, suffix := range []string{"", PreviousSuffix} {
		if suffix == PreviousSuffix && !commit.Previous {
			continue
		}
		file, err := openStartupMarker(path + suffix)
		if err != nil {
			return fmt.Errorf("open published diagnostics %s: %w", path+suffix, err)
		}
		_, statErr := regularFile(file)
		if err := errors.Join(statErr, file.Close()); err != nil {
			return fmt.Errorf("validate published diagnostics %s: %w", path+suffix, err)
		}
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
