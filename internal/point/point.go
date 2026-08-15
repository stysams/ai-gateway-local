// Package point owns transactional client configuration point and restore.
package point

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ai-gateway/internal/point/claude"
	"ai-gateway/internal/point/codex"
	"ai-gateway/internal/point/grok"
)

type Client string

const (
	ClientCodex  Client = "codex"
	ClientClaude Client = "claude"
	ClientGrok   Client = "grok"
)

type State string

const (
	StatePointed            State = "pointed"
	StateNotPointed         State = "not_pointed"
	StateDrifted            State = "drifted"
	StateClientNotInstalled State = "client_not_installed"
	StateUnknown            State = "unknown"
)

var (
	ErrClientNotInstalled = errors.New("client is not installed")
	ErrNoRestore          = errors.New("no unrestored point backup is available")
)

type Options struct {
	HomeDir        string
	LookupEnv      func(string) (string, bool)
	CommandExists  func(string) bool
	Environment    UserEnvironment
	Now            func() time.Time
	AfterFileWrite func() error
}

type Manager struct {
	dataRoot       string
	home           string
	lookupEnv      func(string) (string, bool)
	commandExists  func(string) bool
	environment    UserEnvironment
	now            func() time.Time
	afterFileWrite func() error
	mu             sync.Mutex
}

func New(dataRoot string) *Manager {
	home, _ := os.UserHomeDir()
	return NewWithOptions(dataRoot, Options{HomeDir: home, LookupEnv: os.LookupEnv, CommandExists: commandExists, Environment: SystemEnvironment(), Now: time.Now})
}

func NewWithOptions(dataRoot string, opts Options) *Manager {
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	if opts.CommandExists == nil {
		opts.CommandExists = commandExists
	}
	if opts.Environment == nil {
		opts.Environment = SystemEnvironment()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Manager{dataRoot: dataRoot, home: opts.HomeDir, lookupEnv: opts.LookupEnv, commandExists: opts.CommandExists, environment: opts.Environment, now: opts.Now, afterFileWrite: opts.AfterFileWrite}
}

func commandExists(name string) bool { _, err := exec.LookPath(name); return err == nil }

type Status struct {
	Client          Client `json:"client"`
	PointState      State  `json:"point_state"`
	Target          string `json:"target"`
	BackupAvailable bool   `json:"backup_available"`
	Message         string `json:"message,omitempty"`
}

type Result struct {
	Status
	BackupDir string `json:"backup_dir,omitempty"`
	Changed   bool   `json:"changed"`
}

type PartialFailureError struct {
	Operation, BackupDir string
	Cause, Rollback      error
}

func (e *PartialFailureError) Error() string {
	return fmt.Sprintf("%s failed (%v) and rollback failed (%v); backup: %s", e.Operation, e.Cause, e.Rollback, e.BackupDir)
}
func (e *PartialFailureError) Unwrap() error { return e.Cause }

type Manifest struct {
	Version     int                   `json:"version"`
	Client      Client                `json:"client"`
	CreatedAt   time.Time             `json:"created_at"`
	Completed   bool                  `json:"completed"`
	RestoredAt  *time.Time            `json:"restored_at"`
	Files       []ManifestFile        `json:"files"`
	Environment []ManifestEnvironment `json:"environment,omitempty"`
}

type ManifestFile struct {
	Target         string `json:"target"`
	Backup         string `json:"backup"`
	OriginalExists bool   `json:"original_exists"`
	OriginalSHA256 string `json:"original_sha256,omitempty"`
}

type ManifestEnvironment struct {
	Name           string  `json:"name"`
	OriginalExists bool    `json:"original_exists"`
	OriginalValue  *string `json:"original_value"`
}

func ParseClient(raw string) (Client, error) {
	switch Client(raw) {
	case ClientCodex, ClientClaude, ClientGrok:
		return Client(raw), nil
	default:
		return "", fmt.Errorf("unknown pointable client %q", raw)
	}
}

func (m *Manager) Check(client Client, baseURL string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.check(client, baseURL)
}

func (m *Manager) check(client Client, baseURL string) Status {
	target, installed, err := m.target(client)
	status := Status{Client: client, Target: target, PointState: StateUnknown, BackupAvailable: m.latestManifest(client) != ""}
	if err != nil {
		status.Message = err.Error()
		return status
	}
	if !installed {
		status.PointState = StateClientNotInstalled
		status.Message = ErrClientNotInstalled.Error()
		return status
	}
	data, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		status.PointState = StateNotPointed
		return status
	}
	if err != nil {
		status.Message = err.Error()
		return status
	}
	pointed, err := checkContent(client, data, baseURL)
	if err != nil {
		status.PointState = StateDrifted
		status.Message = err.Error()
		return status
	}
	if pointed && client == ClientCodex {
		value, exists, envErr := m.environment.Lookup(codex.PlaceholderEnvironment)
		if envErr != nil {
			status.Message = envErr.Error()
			return status
		}
		pointed = exists && value == codex.PlaceholderValue
	}
	if pointed {
		status.PointState = StatePointed
		return status
	}
	if status.BackupAvailable {
		status.PointState = StateDrifted
	} else {
		status.PointState = StateNotPointed
	}
	return status
}

func (m *Manager) Point(client Client, baseURL string) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.checkUnlocked(client, baseURL)
	if current.PointState == StatePointed {
		return Result{Status: current, Changed: false}, nil
	}
	if current.PointState == StateClientNotInstalled {
		return Result{Status: current}, ErrClientNotInstalled
	}
	target := current.Target
	original, exists, mode, err := readFile(target)
	if err != nil {
		return Result{Status: current}, err
	}
	modified, err := transformContent(client, original, baseURL)
	if err != nil {
		return Result{Status: current}, err
	}

	manifest := Manifest{Version: 1, Client: client, CreatedAt: m.now().UTC(), Files: []ManifestFile{{Target: target, Backup: filepath.Base(target), OriginalExists: exists}}}
	if exists {
		sum := sha256.Sum256(original)
		manifest.Files[0].OriginalSHA256 = hex.EncodeToString(sum[:])
	}
	if client == ClientCodex {
		value, envExists, envErr := m.environment.Lookup(codex.PlaceholderEnvironment)
		if envErr != nil {
			return Result{Status: current}, envErr
		}
		entry := ManifestEnvironment{Name: codex.PlaceholderEnvironment, OriginalExists: envExists}
		if envExists {
			entry.OriginalValue = &value
		}
		manifest.Environment = []ManifestEnvironment{entry}
	}
	backupDir, err := m.createBackup(manifest, original)
	if err != nil {
		return Result{Status: current}, err
	}
	if err := atomicWrite(target, modified, mode); err != nil {
		return Result{Status: current, BackupDir: backupDir}, err
	}
	fileChanged, envChanged := true, false
	rollback := func(cause error) (Result, error) {
		var rollbackErr error
		if fileChanged {
			rollbackErr = restoreFile(target, original, exists, mode)
		}
		if envChanged && len(manifest.Environment) > 0 {
			if err := restoreEnvironment(m.environment, manifest.Environment[0]); err != nil {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		if rollbackErr != nil {
			return Result{Status: current, BackupDir: backupDir}, &PartialFailureError{Operation: "point", BackupDir: backupDir, Cause: cause, Rollback: rollbackErr}
		}
		return Result{Status: current, BackupDir: backupDir}, cause
	}
	if m.afterFileWrite != nil {
		if err := m.afterFileWrite(); err != nil {
			return rollback(err)
		}
	}
	if client == ClientCodex {
		envChanged = true
		if err := m.environment.Set(codex.PlaceholderEnvironment, codex.PlaceholderValue); err != nil {
			return rollback(err)
		}
	}
	verified := m.checkUnlocked(client, baseURL)
	if verified.PointState != StatePointed {
		return rollback(fmt.Errorf("point verification failed: %s", verified.Message))
	}
	manifest.Completed = true
	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		return rollback(err)
	}
	verified.BackupAvailable = true
	return Result{Status: verified, BackupDir: backupDir, Changed: true}, nil
}

func (m *Manager) checkUnlocked(client Client, baseURL string) Status {
	return m.check(client, baseURL)
}

func (m *Manager) Restore(client Client, baseURL string) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	manifestPath := m.latestManifest(client)
	if manifestPath == "" {
		return Result{Status: m.checkUnlocked(client, baseURL)}, ErrNoRestore
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return Result{}, err
	}
	target, _, err := m.target(client)
	if err != nil {
		return Result{}, err
	}
	if len(manifest.Files) != 1 || filepath.Clean(manifest.Files[0].Target) != filepath.Clean(target) {
		return Result{}, errors.New("backup manifest target does not match the current client target")
	}
	backupDir := filepath.Dir(manifestPath)
	current, currentExists, currentMode, err := readFile(target)
	if err != nil {
		return Result{}, err
	}
	var currentEnv ManifestEnvironment
	if client == ClientCodex {
		v, ok, envErr := m.environment.Lookup(codex.PlaceholderEnvironment)
		if envErr != nil {
			return Result{}, envErr
		}
		currentEnv = ManifestEnvironment{Name: codex.PlaceholderEnvironment, OriginalExists: ok}
		if ok {
			currentEnv.OriginalValue = &v
		}
	}
	original, err := loadOriginal(backupDir, manifest.Files[0])
	if err != nil {
		return Result{}, err
	}
	if err := restoreFile(target, original, manifest.Files[0].OriginalExists, currentMode); err != nil {
		return Result{}, err
	}
	envChanged := false
	rollback := func(cause error) (Result, error) {
		rb := restoreFile(target, current, currentExists, currentMode)
		if envChanged && client == ClientCodex {
			rb = errors.Join(rb, restoreEnvironment(m.environment, currentEnv))
		}
		if rb != nil {
			return Result{BackupDir: backupDir}, &PartialFailureError{Operation: "restore", BackupDir: backupDir, Cause: cause, Rollback: rb}
		}
		return Result{BackupDir: backupDir}, cause
	}
	if len(manifest.Environment) > 0 {
		envChanged = true
		if err := restoreEnvironment(m.environment, manifest.Environment[0]); err != nil {
			return rollback(err)
		}
	}
	now := m.now().UTC()
	manifest.RestoredAt = &now
	if err := writeManifest(manifestPath, manifest); err != nil {
		return rollback(err)
	}
	status := m.checkUnlocked(client, baseURL)
	return Result{Status: status, BackupDir: backupDir, Changed: true}, nil
}

func (m *Manager) target(client Client) (string, bool, error) {
	if m.home == "" {
		return "", false, errors.New("user home directory is unavailable")
	}
	var dir, file, command string
	switch client {
	case ClientCodex:
		dir, file, command = filepath.Join(m.home, ".codex"), "config.toml", "codex"
	case ClientClaude:
		dir, file, command = filepath.Join(m.home, ".claude"), "settings.json", "claude"
		if override, ok := m.lookupEnv("CLAUDE_CONFIG_DIR"); ok && strings.TrimSpace(override) != "" {
			dir = override
		}
	case ClientGrok:
		dir, file, command = filepath.Join(m.home, ".grok"), "config.toml", "grok"
		if override, ok := m.lookupEnv("GROK_HOME"); ok && strings.TrimSpace(override) != "" {
			dir = override
		}
	default:
		return "", false, fmt.Errorf("unknown pointable client %q", client)
	}
	if !filepath.IsAbs(dir) {
		return "", false, fmt.Errorf("client config directory must be absolute: %s", dir)
	}
	target := filepath.Join(dir, file)
	_, fileErr := os.Stat(target)
	_, dirErr := os.Stat(dir)
	if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
		return target, false, fmt.Errorf("inspect client config: %w", fileErr)
	}
	if dirErr != nil && !errors.Is(dirErr, os.ErrNotExist) {
		return target, false, fmt.Errorf("inspect client config directory: %w", dirErr)
	}
	installed := fileErr == nil || dirErr == nil || m.commandExists(command)
	return target, installed, nil
}

func (m *Manager) createBackup(manifest Manifest, original []byte) (string, error) {
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(m.dataRoot, "backups", string(manifest.Client), manifest.CreatedAt.Format("20060102T150405.000000000Z")+"-"+hex.EncodeToString(nonce[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	if manifest.Files[0].OriginalExists {
		if err := atomicWrite(filepath.Join(dir, manifest.Files[0].Backup), original, 0o600); err != nil {
			return dir, err
		}
	}
	if err := writeManifest(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return dir, err
	}
	return dir, nil
}

func (m *Manager) latestManifest(client Client) string {
	root := filepath.Join(m.dataRoot, "backups", string(client))
	var candidates []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == "manifest.json" {
			candidates = append(candidates, path)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(candidates)))
	for _, path := range candidates {
		manifest, err := readManifest(path)
		if err == nil && manifest.Version == 1 && manifest.Client == client && manifest.Completed && manifest.RestoredAt == nil {
			return path
		}
	}
	return ""
}

func transformContent(client Client, data []byte, baseURL string) ([]byte, error) {
	switch client {
	case ClientCodex:
		return codex.Transform(data, baseURL)
	case ClientClaude:
		return claude.Transform(data, baseURL)
	case ClientGrok:
		return grok.Transform(data, baseURL)
	}
	return nil, errors.New("unknown client")
}

func checkContent(client Client, data []byte, baseURL string) (bool, error) {
	switch client {
	case ClientCodex:
		return codex.Check(data, baseURL)
	case ClientClaude:
		return claude.Check(data, baseURL)
	case ClientGrok:
		return grok.Check(data, baseURL)
	}
	return false, errors.New("unknown client")
}
