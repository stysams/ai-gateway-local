package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// ErrNotExist is returned by Load when the config file is absent. It is the
// caller's signal to generate the default configuration.
var ErrNotExist = errors.New("config file does not exist")

// ParseError wraps a YAML decoding failure. File is filled in by the caller
// that knows the path, so stderr always carries a locatable file.
type ParseError struct {
	File string
	Err  error
}

func (e *ParseError) Error() string {
	if e.File != "" {
		return fmt.Sprintf("config parse error (%s): %v", e.File, e.Err)
	}
	return fmt.Sprintf("config parse error: %v", e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// knownTopLevel is the set of declared top-level fields. When writing back, a
// key that somehow landed in Extra and collides with a declared field is
// dropped (it can never arrive there through decoding).
var knownTopLevel = map[string]bool{
	"version": true, "listen": true, "logging": true, "ui": true,
	"autostart": true, "providers": true, "routes": true,
}

// Manager serializes reads and writes of one config file and keeps the
// in-process snapshot that the data plane reads from.
type Manager struct {
	mu       sync.Mutex
	path     string
	snapshot *Config
}

// NewManager returns a Manager for the config file at path.
func NewManager(path string) *Manager { return &Manager{path: path} }

// Path returns the config file path.
func (m *Manager) Path() string { return m.path }

// ConfigPath returns the config file path inside a data root.
func ConfigPath(dataDir string) string { return filepath.Join(dataDir, ConfigFileName) }

// Load reads, normalizes and fully validates the config file. A missing file
// yields ErrNotExist; a file that cannot be parsed or fails validation yields
// an error carrying the file path and locatable fields. Load never writes.
func (m *Manager) Load() (*Config, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotExist
		}
		return nil, fmt.Errorf("read config %s: %w", m.path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		if ve, ok := err.(*ValidationError); ok {
			ve.File = m.path
		}
		if pe, ok := err.(*ParseError); ok {
			pe.File = m.path
		}
		return nil, err
	}
	m.mu.Lock()
	m.snapshot = cfg.clone()
	m.mu.Unlock()
	return cfg, nil
}

// LoadOrCreate loads the config, or generates the default config, writes it
// atomically and returns it when the file does not exist yet.
func (m *Manager) LoadOrCreate() (*Config, error) {
	cfg, err := m.Load()
	if errors.Is(err, ErrNotExist) {
		def := Defaults()
		if err := m.Write(def); err != nil {
			return nil, fmt.Errorf("generate default config %s: %w", m.path, err)
		}
		return def, nil
	}
	return cfg, err
}

// Snapshot returns a deep copy of the last successfully loaded or written
// config, or nil if none has been set.
func (m *Manager) Snapshot() *Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshot == nil {
		return nil
	}
	return m.snapshot.clone()
}

// Parse decodes, normalizes and validates config bytes without touching the
// file system.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, &ParseError{Err: err}
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Write persists cfg following the atomic write procedure from
// docs/v1-scheme.md §5.3:
//
//  1. validate the in-memory config,
//  2. marshal to YAML,
//  3. write a unique temp file in the target directory,
//  4. fsync the temp file,
//  5. atomically rename over config.yaml,
//  6. best-effort fsync of the parent directory,
//  7. only then update the in-process snapshot.
//
// Concurrent writers are serialized by the Manager mutex.
func (m *Manager) Write(cfg *Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp config %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, m.path); err != nil {
		return fmt.Errorf("replace config %s: %w", m.path, err)
	}
	tmpName = "" // renamed; defer must not remove the temp name

	syncDir(dir)

	m.snapshot = cfg.clone()
	return nil
}

// Marshal renders cfg to YAML, merging retained unknown top-level fields back
// in. Declared fields always win over Extra on key collision.
func Marshal(cfg *Config) ([]byte, error) {
	c := cfg.clone()
	if len(c.Extra) > 0 {
		for k := range c.Extra {
			if knownTopLevel[k] {
				delete(c.Extra, k)
			}
		}
	}
	return yaml.Marshal(c)
}

// syncDir flushes directory metadata when the platform supports it. It is
// best-effort: Windows does not expose a meaningful directory fsync, and the
// rename above is already atomic.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}
