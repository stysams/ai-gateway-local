package point

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func readFile(path string) ([]byte, bool, os.FileMode, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, 0o600, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, 0, err
	}
	return data, true, info.Mode().Perm(), nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".ai-gateway-point-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

func restoreFile(path string, data []byte, exists bool, mode os.FileMode) error {
	if exists {
		return atomicWrite(path, data, mode)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func restoreEnvironment(env UserEnvironment, original ManifestEnvironment) error {
	if original.OriginalExists {
		if original.OriginalValue == nil {
			return errors.New("environment manifest is missing original_value")
		}
		return env.Set(original.Name, *original.OriginalValue)
	}
	return env.Unset(original.Name)
}

func writeManifest(path string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), 0o600)
}

func readManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func loadOriginal(backupDir string, file ManifestFile) ([]byte, error) {
	if !file.OriginalExists {
		return nil, nil
	}
	if filepath.Base(file.Backup) != file.Backup || file.Backup == "" {
		return nil, errors.New("invalid backup file name")
	}
	data, err := os.ReadFile(filepath.Join(backupDir, file.Backup))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != file.OriginalSHA256 {
		return nil, errors.New("backup SHA-256 does not match manifest")
	}
	return data, nil
}
