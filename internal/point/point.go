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
	"ai-gateway/internal/point/clientcatalog"
	"ai-gateway/internal/point/codex"
	"ai-gateway/internal/point/grok"

	"github.com/pelletier/go-toml/v2"
)

// Settings is what the caller wants a pointed client configuration to express.
type Settings = clientcatalog.Settings

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
	// LoadCodexBundledCatalog, when set, supplies the native Codex catalog
	// used to clone /model rows. Tests inject a fixture; production leaves
	// it nil so the live `codex debug models --bundled` path is used.
	LoadCodexBundledCatalog func() ([]byte, error)
}

type Manager struct {
	dataRoot                string
	home                    string
	lookupEnv               func(string) (string, bool)
	commandExists           func(string) bool
	environment             UserEnvironment
	now                     func() time.Time
	afterFileWrite          func() error
	loadCodexBundledCatalog func() ([]byte, error)
	mu                      sync.Mutex
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
	return &Manager{dataRoot: dataRoot, home: opts.HomeDir, lookupEnv: opts.LookupEnv, commandExists: opts.CommandExists, environment: opts.Environment, now: opts.Now, afterFileWrite: opts.AfterFileWrite, loadCodexBundledCatalog: opts.LoadCodexBundledCatalog}
}

func commandExists(name string) bool { _, err := exec.LookPath(name); return err == nil }

type Status struct {
	Client           Client `json:"client"`
	PointState       State  `json:"point_state"`
	Target           string `json:"target"`
	BackupAvailable  bool   `json:"backup_available"`
	Message          string `json:"message,omitempty"`
	RemoteCompaction *bool  `json:"remote_compaction,omitempty"`
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

func (m *Manager) Check(client Client, baseURL string, settings Settings) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.check(client, baseURL, settings)
}

func (m *Manager) check(client Client, baseURL string, settings Settings) Status {
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
	pointed, err := checkContent(client, data, baseURL, settings, target)
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

func (m *Manager) Point(client Client, baseURL string, settings Settings) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A configuration this gateway already owns is updated in place. Creating a
	// second restore point over it would lose the user's pre-point configuration
	// (docs/v1-scheme.md §12.1).
	synced, err := m.syncSettingsLocked(client, baseURL, settings)
	if err != nil {
		return Result{}, err
	}
	if synced {
		status := m.checkUnlocked(client, baseURL, settings)
		if status.PointState != StatePointed {
			return Result{Status: status}, fmt.Errorf("point update verification failed: %s", status.Message)
		}
		return Result{Status: status, Changed: true}, nil
	}
	current := m.checkUnlocked(client, baseURL, settings)
	if current.PointState == StatePointed {
		return Result{Status: current, Changed: false}, nil
	}
	if current.PointState == StateClientNotInstalled {
		return Result{Status: current}, ErrClientNotInstalled
	}
	target := current.Target
	original, _, _, err := readFile(target)
	if err != nil {
		return Result{Status: current}, err
	}
	planned, err := m.planClientWrite(client, target, original, baseURL, settings)
	if err != nil {
		return Result{Status: current}, err
	}

	manifest := Manifest{Version: 1, Client: client, CreatedAt: m.now().UTC(), Files: planned.files}
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
	backupDir, err := m.createBackup(manifest, planned.originals)
	if err != nil {
		return Result{Status: current}, err
	}
	if err := planned.apply(); err != nil {
		return Result{Status: current, BackupDir: backupDir}, err
	}
	if client == ClientCodex {
		codex.InvalidateModelsCache(filepath.Join(filepath.Dir(target), codex.ModelsCacheName))
	}
	fileChanged, envChanged := true, false
	rollback := func(cause error) (Result, error) {
		var rollbackErr error
		if fileChanged {
			rollbackErr = planned.restore()
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
	verified := m.checkUnlocked(client, baseURL, settings)
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

func (m *Manager) checkUnlocked(client Client, baseURL string, settings Settings) Status {
	return m.check(client, baseURL, settings)
}

// SyncSettings updates an already managed client configuration when its route
// or the enabled model catalog changes. It deliberately does not create a new
// restore point: the existing manifest must continue to restore the client
// configuration that existed before ai-gateway first pointed it.
func (m *Manager) SyncSettings(client Client, baseURL string, settings Settings) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.syncSettingsLocked(client, baseURL, settings)
}

func (m *Manager) syncSettingsLocked(client Client, baseURL string, settings Settings) (bool, error) {
	target, installed, err := m.target(client)
	if err != nil {
		return false, err
	}
	if !installed {
		return false, nil
	}
	original, exists, _, err := readFile(target)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	// Any configuration ai-gateway owns is eligible, whichever preferred model
	// or catalog generation it currently holds. Configurations written by earlier
	// releases are therefore migrated in place rather than re-pointed.
	managed, err := managedContent(client, original, baseURL)
	if err != nil {
		return false, err
	}
	if !managed {
		return false, nil
	}
	if client == ClientCodex {
		value, exists, envErr := m.environment.Lookup(codex.PlaceholderEnvironment)
		if envErr != nil {
			return false, envErr
		}
		if !exists || value != codex.PlaceholderValue {
			return false, nil
		}
	}
	current, err := checkContent(client, original, baseURL, settings, target)
	if err != nil {
		return false, err
	}
	if current {
		return false, nil
	}
	planned, err := m.planClientWrite(client, target, original, baseURL, settings)
	if err != nil {
		return false, err
	}
	if err := planned.apply(); err != nil {
		return false, err
	}
	if client == ClientCodex {
		codex.InvalidateModelsCache(filepath.Join(filepath.Dir(target), codex.ModelsCacheName))
	}
	verified, err := checkContent(client, planned.configBytes, baseURL, settings, target)
	if err != nil || !verified {
		rollbackErr := planned.restore()
		if err == nil {
			err = errors.New("client settings verification failed")
		}
		if rollbackErr != nil {
			return false, &PartialFailureError{Operation: "sync client settings", Cause: err, Rollback: rollbackErr}
		}
		return false, err
	}
	return true, nil
}

func (m *Manager) Restore(client Client, baseURL string, settings Settings) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	manifestPath := m.latestManifest(client)
	if manifestPath == "" {
		return Result{Status: m.checkUnlocked(client, baseURL, settings)}, ErrNoRestore
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return Result{}, err
	}
	target, _, err := m.target(client)
	if err != nil {
		return Result{}, err
	}
	if len(manifest.Files) < 1 {
		return Result{}, errors.New("backup manifest has no files")
	}
	primaryFound := false
	for _, file := range manifest.Files {
		if filepath.Clean(file.Target) == filepath.Clean(target) {
			primaryFound = true
			break
		}
	}
	if !primaryFound {
		return Result{}, errors.New("backup manifest target does not match the current client target")
	}
	backupDir := filepath.Dir(manifestPath)
	type liveFile struct {
		path   string
		data   []byte
		exists bool
		mode   os.FileMode
	}
	live := make([]liveFile, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		data, exists, mode, readErr := readFile(file.Target)
		if readErr != nil {
			return Result{}, readErr
		}
		live = append(live, liveFile{path: file.Target, data: data, exists: exists, mode: mode})
	}
	_, _, currentMode, err := readFile(target)
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
	for i, file := range manifest.Files {
		original, loadErr := loadOriginal(backupDir, file)
		if loadErr != nil {
			return Result{}, loadErr
		}
		mode := currentMode
		if i < len(live) {
			mode = live[i].mode
		}
		if err := restoreFile(file.Target, original, file.OriginalExists, mode); err != nil {
			return Result{}, err
		}
	}
	envChanged := false
	rollback := func(cause error) (Result, error) {
		var rb error
		for _, item := range live {
			rb = errors.Join(rb, restoreFile(item.path, item.data, item.exists, item.mode))
		}
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
	if client == ClientCodex {
		m.cleanupUnreferencedCodexCatalog(target)
		codex.InvalidateModelsCache(filepath.Join(filepath.Dir(target), codex.ModelsCacheName))
	}
	if client == ClientClaude {
		m.cleanupUnreferencedClaudeCache(target, baseURL, manifest.Files)
	}
	status := m.checkUnlocked(client, baseURL, settings)
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
		if override, ok := m.lookupEnv("CODEX_HOME"); ok && strings.TrimSpace(override) != "" {
			dir = override
		}
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

func (m *Manager) createBackup(manifest Manifest, originals map[string][]byte) (string, error) {
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(m.dataRoot, "backups", string(manifest.Client), manifest.CreatedAt.Format("20060102T150405.000000000Z")+"-"+hex.EncodeToString(nonce[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	for _, file := range manifest.Files {
		if !file.OriginalExists {
			continue
		}
		data := originals[file.Target]
		if err := atomicWrite(filepath.Join(dir, file.Backup), data, 0o600); err != nil {
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

func transformContent(client Client, data []byte, baseURL string, settings Settings, catalogPath string) ([]byte, error) {
	switch client {
	case ClientCodex:
		return codex.Transform(data, baseURL, settings, catalogPath)
	case ClientClaude:
		return claude.Transform(data, baseURL, settings)
	case ClientGrok:
		return grok.Transform(data, baseURL, settings)
	}
	return nil, errors.New("unknown client")
}

func checkContent(client Client, data []byte, baseURL string, settings Settings, target string) (bool, error) {
	switch client {
	case ClientCodex:
		return codex.Check(data, baseURL, settings)
	case ClientClaude:
		ok, err := claude.Check(data, baseURL, settings)
		if err != nil || !ok {
			return ok, err
		}
		return claude.CacheMatches(claude.CachePath(target), baseURL, settings)
	case ClientGrok:
		return grok.Check(data, baseURL, settings)
	}
	return false, errors.New("unknown client")
}

// managedContent reports whether ai-gateway owns this configuration, regardless
// of which preferred model or catalog generation it holds.
func managedContent(client Client, data []byte, baseURL string) (bool, error) {
	switch client {
	case ClientCodex:
		return codex.Managed(data, baseURL)
	case ClientClaude:
		return claude.Managed(data, baseURL)
	case ClientGrok:
		return grok.Managed(data, baseURL)
	}
	return false, errors.New("unknown client")
}

type clientWrite struct {
	configPath  string
	configBytes []byte
	configMode  os.FileMode
	files       []ManifestFile
	originals   map[string][]byte
	writes      []plannedWrite
}

type plannedWrite struct {
	path     string
	data     []byte
	original []byte
	exists   bool
	mode     os.FileMode
}

func (w clientWrite) apply() error {
	for _, item := range w.writes {
		if err := atomicWrite(item.path, item.data, item.mode); err != nil {
			_ = w.restore()
			return err
		}
	}
	return nil
}

func (w clientWrite) restore() error {
	var err error
	for i := len(w.writes) - 1; i >= 0; i-- {
		item := w.writes[i]
		err = errors.Join(err, restoreFile(item.path, item.original, item.exists, item.mode))
	}
	return err
}

func (m *Manager) planClientWrite(client Client, target string, original []byte, baseURL string, settings Settings) (clientWrite, error) {
	_, exists, mode, err := readFile(target)
	if err != nil {
		return clientWrite{}, err
	}
	if exists && original == nil {
		original, _, _, err = readFile(target)
		if err != nil {
			return clientWrite{}, err
		}
	}
	plan := clientWrite{
		configPath: target,
		configMode: mode,
		originals:  map[string][]byte{},
	}
	catalogPath := ""
	var catalogBytes []byte
	if client == ClientCodex {
		catalogPath = codex.CatalogPath(target)
		template, loadErr := codex.LoadTemplate(m.loadCodexBundledCatalog, filepath.Join(filepath.Dir(target), codex.ModelsCacheName))
		if loadErr != nil {
			return clientWrite{}, loadErr
		}
		catalogBytes, err = codex.BuildCatalog(template, settings)
		if err != nil {
			return clientWrite{}, err
		}
	}
	var claudeCache []byte
	var claudeCachePath string
	if client == ClientClaude {
		claudeCachePath = claude.CachePath(target)
		claudeCache, err = claude.BuildCache(baseURL, settings, m.now())
		if err != nil {
			return clientWrite{}, err
		}
	}
	modified, err := transformContent(client, original, baseURL, settings, catalogPath)
	if err != nil {
		return clientWrite{}, err
	}
	plan.configBytes = modified
	plan.addFile(target, original, exists, mode, modified)
	if client == ClientCodex {
		catOriginal, catExists, catMode, catErr := readFile(catalogPath)
		if catErr != nil {
			return clientWrite{}, catErr
		}
		plan.addFile(catalogPath, catOriginal, catExists, catMode, catalogBytes)
	}
	if client == ClientClaude {
		cacheOriginal, cacheExists, cacheMode, cacheErr := readFile(claudeCachePath)
		if cacheErr != nil {
			return clientWrite{}, cacheErr
		}
		plan.addFile(claudeCachePath, cacheOriginal, cacheExists, cacheMode, claudeCache)
	}
	return plan, nil
}

func (w *clientWrite) addFile(path string, original []byte, exists bool, mode os.FileMode, next []byte) {
	entry := ManifestFile{Target: path, Backup: filepath.Base(path), OriginalExists: exists}
	if exists {
		sum := sha256.Sum256(original)
		entry.OriginalSHA256 = hex.EncodeToString(sum[:])
		w.originals[path] = original
	}
	w.files = append(w.files, entry)
	if mode == 0 {
		mode = 0o600
	}
	w.writes = append(w.writes, plannedWrite{path: path, data: next, original: original, exists: exists, mode: mode})
}

func (m *Manager) cleanupUnreferencedClaudeCache(settingsPath, baseURL string, files []ManifestFile) {
	owned := claude.CachePath(settingsPath)
	for _, file := range files {
		if filepath.Clean(file.Target) == filepath.Clean(owned) {
			return
		}
	}
	_ = claude.RemoveOwnedCache(owned, baseURL)
}

func (m *Manager) cleanupUnreferencedCodexCatalog(configPath string) {
	owned := codex.CatalogPath(configPath)
	data, exists, _, err := readFile(configPath)
	if err != nil || !exists {
		_ = os.Remove(owned)
		return
	}
	doc := map[string]any{}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return
	}
	path, _ := doc["model_catalog_json"].(string)
	if filepath.Clean(path) != filepath.Clean(owned) {
		_ = os.Remove(owned)
	}
}
