package point

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ai-gateway/internal/point/claudedesktop"
)

func (m *Manager) desktopRoot(baseURL string) (claudedesktop.Root, bool, error) {
	roots, err := claudedesktop.Discover(m.lookupEnv, "", "")
	if err != nil {
		return claudedesktop.Root{}, false, err
	}
	if len(roots) == 0 {
		return claudedesktop.Root{}, false, nil
	}
	root, err := claudedesktop.Select(roots, baseURL)
	if err != nil {
		return claudedesktop.Root{}, true, err
	}
	return root, true, nil
}

func (m *Manager) checkClaudeDesktop(baseURL string, settings Settings) Status {
	root, installed, err := m.desktopRoot(baseURL)
	status := Status{
		Client:          ClientClaudeDesktop,
		PointState:      StateUnknown,
		MCPPointState:   StateUnknown,
		BackupAvailable: m.latestDesktopManifest(false) != "",
		MCPBackup:       m.latestDesktopManifest(true) != "",
	}
	if err != nil {
		status.Message = err.Error()
		status.MCPMessage = err.Error()
		return status
	}
	if !installed {
		status.PointState = StateClientNotInstalled
		status.MCPPointState = StateClientNotInstalled
		status.Message = ErrClientNotInstalled.Error()
		status.MCPMessage = ErrClientNotInstalled.Error()
		return status
	}
	status.Target = desktopTarget(root)
	status.MCPTarget = root.ControlPath
	manifestPath := m.latestDesktopManifestAny()
	if manifestPath != "" {
		manifest, manifestErr := readManifest(manifestPath)
		if manifestErr == nil {
			status.RestoredAt = manifest.RestoredAt
			status.MCPRestoredAt = manifest.MCPRestoredAt
		}
	}

	if root.ProfilePath == "" || !root.ProfileExists {
		status.PointState = StateNotPointed
	} else {
		profile, readErr := os.ReadFile(root.ProfilePath)
		if readErr != nil {
			status.PointState = StateUnknown
			status.Message = readErr.Error()
		} else {
			managed, managedErr := claudedesktop.Managed(profile, baseURL)
			switch {
			case managedErr != nil:
				status.PointState = StateDrifted
				status.Message = managedErr.Error()
			case !managed:
				status.PointState = desktopUnpointedState(status.BackupAvailable)
				status.Message = "Claude Desktop profile is not managed by ai-gateway"
			default:
				pointed, checkErr := claudedesktop.CheckProfile(profile, baseURL, settings)
				switch {
				case checkErr != nil:
					status.PointState = StateDrifted
					status.Message = checkErr.Error()
				case pointed:
					status.PointState = StatePointed
				default:
					status.PointState = desktopUnpointedState(status.BackupAvailable)
				}
			}
		}
	}

	control, exists, _, readErr := readFile(root.ControlPath)
	if readErr != nil {
		status.MCPPointState = StateUnknown
		status.MCPMessage = readErr.Error()
		return status
	}
	if !exists {
		status.MCPPointState = StateNotPointed
		return status
	}
	controlPointed, controlErr := claudedesktop.CheckControl(control, true)
	if controlErr != nil {
		status.MCPPointState = StateDrifted
		status.MCPMessage = controlErr.Error()
		return status
	}
	status.MCPPointState = StatePointed
	if !controlPointed {
		status.MCPPointState = StateDrifted
		status.MCPMessage = "Claude Desktop deployment mode is not 3p"
	}
	if manifestPath == "" {
		return status
	}
	manifest, manifestErr := readManifest(manifestPath)
	if manifestErr != nil {
		status.MCPPointState = StateUnknown
		status.MCPMessage = manifestErr.Error()
		return status
	}
	controlFile := manifestFileAt(manifest, root.ControlPath)
	if controlFile == nil {
		status.MCPPointState = StateUnknown
		status.MCPMessage = "Claude Desktop backup does not contain the control file"
		return status
	}
	original, loadErr := loadOriginal(filepath.Dir(manifestPath), *controlFile)
	if loadErr != nil {
		status.MCPPointState = StateUnknown
		status.MCPMessage = loadErr.Error()
		return status
	}
	matches, matchErr := claudedesktop.MCPMatches(control, true, original, controlFile.OriginalExists)
	if matchErr != nil {
		status.MCPPointState = StateDrifted
		status.MCPMessage = matchErr.Error()
	} else if !matches {
		status.MCPPointState = StateDrifted
		status.MCPMessage = "Claude Desktop MCP configuration has drifted"
	}
	return status
}

func desktopTarget(root claudedesktop.Root) string {
	if root.ProfilePath != "" {
		return root.ProfilePath
	}
	return filepath.Join(root.ProfileDir, "<active-profile>.json")
}

func desktopUnpointedState(hasBackup bool) State {
	if hasBackup {
		return StateDrifted
	}
	return StateNotPointed
}

func (m *Manager) pointClaudeDesktop(baseURL string, settings Settings) (Result, error) {
	root, installed, err := m.desktopRoot(baseURL)
	if err != nil {
		return Result{Status: Status{Client: ClientClaudeDesktop, PointState: StateUnknown, Message: err.Error()}}, err
	}
	if !installed {
		status := Status{Client: ClientClaudeDesktop, PointState: StateClientNotInstalled, MCPPointState: StateClientNotInstalled, Message: ErrClientNotInstalled.Error()}
		return Result{Status: status}, ErrClientNotInstalled
	}
	if root.ProfilePath != "" && root.ProfileExists {
		profile, readErr := os.ReadFile(root.ProfilePath)
		if readErr != nil {
			return Result{Status: Status{Client: ClientClaudeDesktop, Target: root.ProfilePath}}, readErr
		}
		managed, managedErr := claudedesktop.Managed(profile, baseURL)
		if managedErr != nil {
			return Result{Status: Status{Client: ClientClaudeDesktop, Target: root.ProfilePath}}, managedErr
		}
		if managed {
			changed, syncErr := m.syncClaudeDesktop(baseURL, settings)
			if syncErr != nil {
				return Result{Status: m.checkClaudeDesktop(baseURL, settings)}, syncErr
			}
			status := m.checkClaudeDesktop(baseURL, settings)
			return Result{Status: status, Changed: changed}, nil
		}
	}

	profileID, profilePath, profileOriginal, profileExists, profileMode, err := m.newDesktopProfile(root, baseURL)
	if err != nil {
		return Result{Status: Status{Client: ClientClaudeDesktop, Target: desktopTarget(root)}}, err
	}
	metaOriginal, metaExists, metaMode, err := readFile(root.MetaPath)
	if err != nil {
		return Result{Status: Status{Client: ClientClaudeDesktop, Target: profilePath}}, err
	}
	controlOriginal, controlExists, controlMode, err := readFile(root.ControlPath)
	if err != nil {
		return Result{Status: Status{Client: ClientClaudeDesktop, Target: profilePath}}, err
	}
	profileNext, err := claudedesktop.TransformProfile(profileOriginal, baseURL, settings)
	if err != nil {
		return Result{Status: Status{Client: ClientClaudeDesktop, Target: profilePath}}, err
	}
	metaNext, err := claudedesktop.TransformMeta(metaOriginal, profileID, claudedesktop.DefaultProfileName)
	if err != nil {
		return Result{Status: Status{Client: ClientClaudeDesktop, Target: profilePath}}, err
	}
	controlNext, err := claudedesktop.TransformControl(controlOriginal)
	if err != nil {
		return Result{Status: Status{Client: ClientClaudeDesktop, Target: profilePath}}, err
	}
	plan := clientWrite{configPath: profilePath, configBytes: profileNext, configMode: profileMode, originals: map[string][]byte{}}
	plan.addFile(profilePath, profileOriginal, profileExists, profileMode, profileNext)
	plan.addFile(root.MetaPath, metaOriginal, metaExists, metaMode, metaNext)
	plan.addFile(root.ControlPath, controlOriginal, controlExists, controlMode, controlNext)
	manifest := Manifest{Version: 1, Client: ClientClaudeDesktop, CreatedAt: m.now().UTC(), Files: plan.files}
	backupDir, err := m.createBackup(manifest, plan.originals)
	if err != nil {
		return Result{Status: Status{Client: ClientClaudeDesktop, Target: profilePath}}, err
	}
	if err := plan.apply(); err != nil {
		return Result{Status: Status{Client: ClientClaudeDesktop, Target: profilePath}, BackupDir: backupDir}, err
	}
	rollback := func(cause error) (Result, error) {
		if rollbackErr := plan.restore(); rollbackErr != nil {
			return Result{Status: Status{Client: ClientClaudeDesktop, Target: profilePath}, BackupDir: backupDir}, &PartialFailureError{Operation: "point Claude Desktop", BackupDir: backupDir, Cause: cause, Rollback: rollbackErr}
		}
		return Result{Status: Status{Client: ClientClaudeDesktop, Target: profilePath}, BackupDir: backupDir}, cause
	}
	if m.afterFileWrite != nil {
		if err := m.afterFileWrite(); err != nil {
			return rollback(err)
		}
	}
	verified := m.checkClaudeDesktop(baseURL, settings)
	if verified.PointState != StatePointed || verified.MCPPointState != StatePointed {
		message := verified.Message
		if message == "" {
			message = verified.MCPMessage
		}
		return rollback(fmt.Errorf("Claude Desktop point verification failed: %s", message))
	}
	manifest.Completed = true
	if err := writeManifest(filepath.Join(backupDir, "manifest.json"), manifest); err != nil {
		return rollback(err)
	}
	verified.BackupAvailable = true
	verified.MCPBackup = true
	return Result{Status: verified, BackupDir: backupDir, Changed: true}, nil
}

func (m *Manager) syncClaudeDesktop(baseURL string, settings Settings) (bool, error) {
	root, installed, err := m.desktopRoot(baseURL)
	if err != nil {
		return false, err
	}
	if !installed || root.ProfilePath == "" || !root.ProfileExists {
		return false, nil
	}
	profileOriginal, profileExists, profileMode, err := readFile(root.ProfilePath)
	if err != nil {
		return false, err
	}
	managed, err := claudedesktop.Managed(profileOriginal, baseURL)
	if err != nil {
		return false, err
	}
	if !managed {
		return false, nil
	}
	metaOriginal, metaExists, metaMode, err := readFile(root.MetaPath)
	if err != nil {
		return false, err
	}
	controlOriginal, controlExists, controlMode, err := readFile(root.ControlPath)
	if err != nil {
		return false, err
	}
	profileNext, err := claudedesktop.TransformProfile(profileOriginal, baseURL, settings)
	if err != nil {
		return false, err
	}
	metaNext, err := claudedesktop.TransformMeta(metaOriginal, root.ProfileID, claudedesktop.DefaultProfileName)
	if err != nil {
		return false, err
	}
	controlNext, err := claudedesktop.TransformControl(controlOriginal)
	if err != nil {
		return false, err
	}
	plan := clientWrite{configPath: root.ProfilePath, configBytes: profileNext, configMode: profileMode, originals: map[string][]byte{}}
	addChangedFile(&plan, root.ProfilePath, profileOriginal, profileExists, profileMode, profileNext)
	addChangedFile(&plan, root.MetaPath, metaOriginal, metaExists, metaMode, metaNext)
	addChangedFile(&plan, root.ControlPath, controlOriginal, controlExists, controlMode, controlNext)
	if len(plan.writes) == 0 {
		return false, nil
	}
	if err := plan.apply(); err != nil {
		return false, err
	}
	verified := m.checkClaudeDesktop(baseURL, settings)
	if verified.PointState != StatePointed || verified.MCPPointState != StatePointed {
		verifyErr := fmt.Errorf("Claude Desktop sync verification failed: %s", firstNonEmpty(verified.Message, verified.MCPMessage))
		if rollbackErr := plan.restore(); rollbackErr != nil {
			return false, &PartialFailureError{Operation: "sync Claude Desktop", Cause: verifyErr, Rollback: rollbackErr}
		}
		return false, verifyErr
	}
	return true, nil
}

func addChangedFile(plan *clientWrite, path string, original []byte, exists bool, mode os.FileMode, next []byte) {
	if exists && string(original) == string(next) {
		return
	}
	if !exists && len(next) == 0 {
		return
	}
	plan.addFile(path, original, exists, mode, next)
}

func (m *Manager) newDesktopProfile(root claudedesktop.Root, baseURL string) (string, string, []byte, bool, os.FileMode, error) {
	if root.ProfilePath != "" && root.ProfileExists {
		data, exists, mode, err := readFile(root.ProfilePath)
		if err != nil {
			return "", "", nil, false, 0, err
		}
		managed, err := claudedesktop.Managed(data, baseURL)
		if err != nil {
			return "", "", nil, false, 0, err
		}
		if managed {
			return root.ProfileID, root.ProfilePath, data, exists, mode, nil
		}
	}
	for {
		id, err := claudedesktop.NewProfileID()
		if err != nil {
			return "", "", nil, false, 0, err
		}
		path := filepath.Join(root.ProfileDir, id+".json")
		_, exists, mode, err := readFile(path)
		if err != nil {
			return "", "", nil, false, 0, err
		}
		if !exists {
			return id, path, nil, false, mode, nil
		}
	}
}

func (m *Manager) restoreClaudeDesktop(baseURL string, settings Settings, restoreMCP bool) (Result, error) {
	manifestPath := m.latestDesktopManifest(restoreMCP)
	if manifestPath == "" {
		return Result{Status: m.checkClaudeDesktop(baseURL, settings)}, ErrNoRestore
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return Result{}, err
	}
	root, installed, err := m.desktopRoot(baseURL)
	if err != nil {
		return Result{BackupDir: filepath.Dir(manifestPath)}, err
	}
	if !installed {
		return Result{BackupDir: filepath.Dir(manifestPath)}, ErrClientNotInstalled
	}
	controlFile := manifestFileAt(manifest, root.ControlPath)
	if controlFile == nil {
		return Result{BackupDir: filepath.Dir(manifestPath)}, errors.New("backup manifest does not match Claude Desktop control target")
	}
	controlOriginal, err := loadOriginal(filepath.Dir(manifestPath), *controlFile)
	if err != nil {
		return Result{BackupDir: filepath.Dir(manifestPath)}, err
	}
	currentControl, currentControlExists, controlMode, err := readFile(root.ControlPath)
	if err != nil {
		return Result{BackupDir: filepath.Dir(manifestPath)}, err
	}
	controlNext, controlNextExists, err := claudedesktop.RestoreControl(currentControl, currentControlExists, controlOriginal, controlFile.OriginalExists, restoreMCP)
	if err != nil {
		return Result{BackupDir: filepath.Dir(manifestPath)}, err
	}

	plan := clientWrite{originals: map[string][]byte{}}
	if restoreMCP {
		addRestoreWrite(&plan, root.ControlPath, currentControl, currentControlExists, controlMode, controlNext, controlNextExists)
	} else {
		profileFile := desktopProfileManifestFile(manifest, root.ProfileDir)
		metaFile := manifestFileAt(manifest, root.MetaPath)
		if profileFile == nil || metaFile == nil {
			return Result{BackupDir: filepath.Dir(manifestPath)}, errors.New("backup manifest does not contain Claude Desktop inference files")
		}
		profileOriginal, err := loadOriginal(filepath.Dir(manifestPath), *profileFile)
		if err != nil {
			return Result{BackupDir: filepath.Dir(manifestPath)}, err
		}
		metaOriginal, err := loadOriginal(filepath.Dir(manifestPath), *metaFile)
		if err != nil {
			return Result{BackupDir: filepath.Dir(manifestPath)}, err
		}
		profileCurrent, profileCurrentExists, profileMode, err := readFile(profileFile.Target)
		if err != nil {
			return Result{BackupDir: filepath.Dir(manifestPath)}, err
		}
		metaCurrent, metaCurrentExists, metaMode, err := readFile(metaFile.Target)
		if err != nil {
			return Result{BackupDir: filepath.Dir(manifestPath)}, err
		}
		addRestoreWrite(&plan, profileFile.Target, profileCurrent, profileCurrentExists, profileMode, profileOriginal, profileFile.OriginalExists)
		addRestoreWrite(&plan, metaFile.Target, metaCurrent, metaCurrentExists, metaMode, metaOriginal, metaFile.OriginalExists)
		addRestoreWrite(&plan, root.ControlPath, currentControl, currentControlExists, controlMode, controlNext, controlNextExists)
	}
	if len(plan.writes) == 0 {
		return Result{Status: m.checkClaudeDesktop(baseURL, settings), BackupDir: filepath.Dir(manifestPath), Changed: false}, nil
	}
	oldManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return Result{BackupDir: filepath.Dir(manifestPath)}, err
	}
	if err := plan.apply(); err != nil {
		return Result{BackupDir: filepath.Dir(manifestPath)}, err
	}
	rollback := func(cause error) (Result, error) {
		rb := plan.restore()
		if manifestErr := atomicWrite(manifestPath, oldManifest, 0o600); manifestErr != nil {
			rb = errors.Join(rb, manifestErr)
		}
		if rb != nil {
			return Result{BackupDir: filepath.Dir(manifestPath)}, &PartialFailureError{Operation: "restore Claude Desktop", BackupDir: filepath.Dir(manifestPath), Cause: cause, Rollback: rb}
		}
		return Result{BackupDir: filepath.Dir(manifestPath)}, cause
	}
	now := m.now().UTC()
	if restoreMCP {
		manifest.MCPRestoredAt = &now
	} else {
		manifest.RestoredAt = &now
	}
	if err := writeManifest(manifestPath, manifest); err != nil {
		return rollback(err)
	}
	status := m.checkClaudeDesktop(baseURL, settings)
	return Result{Status: status, BackupDir: filepath.Dir(manifestPath), Changed: true}, nil
}

func addRestoreWrite(plan *clientWrite, path string, current []byte, currentExists bool, mode os.FileMode, next []byte, nextExists bool) {
	if currentExists == nextExists && string(current) == string(next) {
		return
	}
	if mode == 0 {
		mode = 0o600
	}
	plan.writes = append(plan.writes, plannedWrite{path: path, data: next, original: current, exists: currentExists, mode: mode})
}

func manifestFileAt(manifest Manifest, target string) *ManifestFile {
	for i := range manifest.Files {
		if filepath.Clean(manifest.Files[i].Target) == filepath.Clean(target) {
			return &manifest.Files[i]
		}
	}
	return nil
}

func desktopProfileManifestFile(manifest Manifest, profileDir string) *ManifestFile {
	var match *ManifestFile
	for i := range manifest.Files {
		file := &manifest.Files[i]
		if filepath.Clean(filepath.Dir(file.Target)) != filepath.Clean(profileDir) || filepath.Base(file.Target) == claudedesktop.MetaFileName {
			continue
		}
		if match != nil {
			return nil
		}
		match = file
	}
	return match
}

func (m *Manager) latestDesktopManifestAny() string {
	root := filepath.Join(m.dataRoot, "backups", string(ClientClaudeDesktop))
	var candidates []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == "manifest.json" {
			candidates = append(candidates, path)
		}
		return nil
	})
	for i := range candidates {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j] > candidates[i] {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	for _, path := range candidates {
		manifest, err := readManifest(path)
		if err == nil && manifest.Version == 1 && manifest.Client == ClientClaudeDesktop && manifest.Completed && (manifest.RestoredAt == nil || manifest.MCPRestoredAt == nil) {
			return path
		}
	}
	return ""
}

func (m *Manager) latestDesktopManifest(restoreMCP bool) string {
	root := filepath.Join(m.dataRoot, "backups", string(ClientClaudeDesktop))
	var candidates []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == "manifest.json" {
			candidates = append(candidates, path)
		}
		return nil
	})
	for i := range candidates {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j] > candidates[i] {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	for _, path := range candidates {
		manifest, err := readManifest(path)
		if err != nil || manifest.Version != 1 || manifest.Client != ClientClaudeDesktop || !manifest.Completed {
			continue
		}
		if restoreMCP && manifest.MCPRestoredAt == nil {
			return path
		}
		if !restoreMCP && manifest.RestoredAt == nil {
			return path
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unknown error"
}
