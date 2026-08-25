// Package claudedesktop edits Claude Desktop's third-party inference profile.
package claudedesktop

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"ai-gateway/internal/point/clientcatalog"
	"ai-gateway/internal/point/jsonedit"
)

const (
	ProfileDirName     = "configLibrary"
	MetaFileName       = "_meta.json"
	ControlFileName    = "claude_desktop_config.json"
	DeploymentModeKey  = "deploymentMode"
	DeploymentMode3P   = "3p"
	InferenceProvider  = "gateway"
	CredentialKind     = "static"
	AuthScheme         = "bearer"
	APIKeyPlaceholder  = "sk-ai-gateway-local"
	DefaultProfileName = "Claude Desktop (ai-gateway)"
	InferenceBasePath  = "/c/claude-desktop"
)

type Root struct {
	Base             string
	MetaPath         string
	ProfileDir       string
	ProfilePath      string
	ControlPath      string
	ProfileID        string
	ProfileDirExists bool
	MetaExists       bool
	ControlExists    bool
	ProfileExists    bool
}

type Entry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type metaDoc struct {
	AppliedID string  `json:"appliedId"`
	Entries   []Entry `json:"entries"`
}

type profileModel struct {
	Name                string `json:"name"`
	LabelOverride       string `json:"labelOverride"`
	AnthropicFamilyTier string `json:"anthropicFamilyTier,omitempty"`
	Supports1m          bool   `json:"supports1m,omitempty"`
}

type profileDoc struct {
	InferenceProvider          string         `json:"inferenceProvider"`
	InferenceCredentialKind    string         `json:"inferenceCredentialKind"`
	InferenceGatewayBaseURL    string         `json:"inferenceGatewayBaseUrl"`
	InferenceGatewayAuthScheme string         `json:"inferenceGatewayAuthScheme"`
	InferenceGatewayAPIKey     string         `json:"inferenceGatewayApiKey"`
	InferenceModels            []profileModel `json:"inferenceModels"`
	ModelDiscoveryEnabled      bool           `json:"modelDiscoveryEnabled"`
}

// Discover finds supported ordinary Claude Desktop installations. The profile,
// metadata and control paths are always kept under one discovered root.
func Discover(lookupEnv func(string) (string, bool), localAppData string, appData string) ([]Root, error) {
	if strings.TrimSpace(localAppData) == "" && lookupEnv != nil {
		localAppData, _ = lookupEnv("LOCALAPPDATA")
	}
	if strings.TrimSpace(appData) == "" && lookupEnv != nil {
		appData, _ = lookupEnv("APPDATA")
	}
	candidatePaths := []string{
		filepath.Join(localAppData, "Claude"),
		filepath.Join(appData, "Claude"),
		filepath.Join(localAppData, "Claude-3p"),
	}
	seen := make(map[string]struct{}, len(candidatePaths))
	candidates := make([]string, 0, len(candidatePaths))
	for _, root := range candidatePaths {
		if root == "." || root == "" {
			continue
		}
		clean := filepath.Clean(root)
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		info, err := os.Stat(clean)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect Claude Desktop directory %q: %w", clean, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("Claude Desktop path is not a directory: %q", clean)
		}
		candidates = append(candidates, clean)
	}
	return roots(candidates), nil
}

func roots(candidates []string) []Root {
	out := make([]Root, 0, len(candidates))
	for _, base := range candidates {
		profileDir := filepath.Join(base, ProfileDirName)
		metaPath := filepath.Join(profileDir, MetaFileName)
		controlPath := filepath.Join(base, ControlFileName)
		root := Root{Base: base, MetaPath: metaPath, ProfileDir: profileDir, ControlPath: controlPath}
		if info, err := os.Stat(profileDir); err == nil {
			root.ProfileDirExists = info.IsDir()
		}
		if _, err := os.Stat(metaPath); err == nil {
			root.MetaExists = true
		}
		if _, err := os.Stat(controlPath); err == nil {
			root.ControlExists = true
		}
		meta, err := os.ReadFile(metaPath)
		if err != nil {
			out = append(out, root)
			continue
		}
		id, parseErr := appliedID(meta)
		if parseErr != nil || id == "" {
			out = append(out, root)
			continue
		}
		root.ProfileID = id
		root.ProfilePath = filepath.Join(profileDir, id+".json")
		if _, profileErr := os.Stat(root.ProfilePath); profileErr == nil {
			root.ProfileExists = true
		}
		out = append(out, root)
	}
	return out
}

// Select returns the only root with Claude Desktop's profile catalog, the only
// root proven to belong to the current gateway, or the only discovered root.
// Runtime-only directories are ignored when a single profile root is present.
func Select(roots []Root, baseURL string) (Root, error) {
	var managedRoot *Root
	for i := range roots {
		root := &roots[i]
		if root.ProfilePath == "" || !root.ProfileExists {
			continue
		}
		data, err := os.ReadFile(root.ProfilePath)
		if err != nil {
			return Root{}, err
		}
		managed, err := Managed(data, baseURL)
		if err != nil {
			return Root{}, err
		}
		if managed {
			if managedRoot != nil {
				return Root{}, errors.New("multiple Claude Desktop roots are managed by this gateway")
			}
			managedRoot = root
		}
	}
	if managedRoot != nil {
		return *managedRoot, nil
	}
	profileRoots := make([]Root, 0, len(roots))
	for _, root := range roots {
		if root.ProfileDirExists || root.MetaExists || root.ProfileExists {
			profileRoots = append(profileRoots, root)
		}
	}
	if len(profileRoots) == 1 {
		return profileRoots[0], nil
	}
	if len(profileRoots) > 1 {
		return Root{}, errors.New("multiple Claude Desktop profile directories found; refusing to guess the active installation")
	}
	if len(roots) == 1 {
		return roots[0], nil
	}
	if len(roots) == 0 {
		return Root{}, errors.New("Claude Desktop is not installed in a supported directory")
	}
	return Root{}, errors.New("multiple Claude Desktop directories found; refusing to guess the active installation")
}

func BaseURL(gatewayBase string) string {
	return strings.TrimRight(gatewayBase, "/") + InferenceBasePath
}

func Managed(data []byte, gatewayBase string) (bool, error) {
	var doc struct {
		InferenceProvider       string `json:"inferenceProvider"`
		InferenceGatewayBaseURL string `json:"inferenceGatewayBaseUrl"`
	}
	if err := decode(data, &doc); err != nil {
		return false, err
	}
	return doc.InferenceProvider == InferenceProvider && sameGatewayURL(doc.InferenceGatewayBaseURL, BaseURL(gatewayBase)), nil
}

func CheckProfile(data []byte, gatewayBase string, settings clientcatalog.Settings) (bool, error) {
	var doc profileDoc
	if err := decode(data, &doc); err != nil {
		return false, err
	}
	if doc.InferenceProvider != InferenceProvider || doc.InferenceCredentialKind != CredentialKind || doc.InferenceGatewayAuthScheme != AuthScheme || doc.InferenceGatewayAPIKey == "" || !sameGatewayURL(doc.InferenceGatewayBaseURL, BaseURL(gatewayBase)) || doc.ModelDiscoveryEnabled {
		return false, nil
	}
	want := Models(settings)
	if len(doc.InferenceModels) != len(want) {
		return false, nil
	}
	for i := range want {
		if doc.InferenceModels[i] != want[i] {
			return false, nil
		}
	}
	return true, nil
}

func TransformProfile(data []byte, gatewayBase string, settings clientcatalog.Settings) ([]byte, error) {
	if data != nil && len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("Claude Desktop profile is empty")
	}
	profile := profileDoc{
		InferenceProvider: InferenceProvider, InferenceCredentialKind: CredentialKind,
		InferenceGatewayBaseURL: BaseURL(gatewayBase), InferenceGatewayAuthScheme: AuthScheme,
		InferenceGatewayAPIKey: APIKeyPlaceholder, InferenceModels: Models(settings), ModelDiscoveryEnabled: false,
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		return nil, err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, err
	}
	keys := []string{"inferenceProvider", "inferenceCredentialKind", "inferenceGatewayBaseUrl", "inferenceGatewayAuthScheme", "inferenceGatewayApiKey", "inferenceModels", "modelDiscoveryEnabled"}
	raw := make([]jsonedit.RawKV, 0, len(keys))
	for _, key := range keys {
		raw = append(raw, jsonedit.RawKV{Key: key, Value: values[key]})
	}
	return jsonedit.SetRootValues(data, raw)
}

func Models(settings clientcatalog.Settings) []profileModel {
	display := settings.RouteDisplayName
	if display == "" {
		display = settings.Model()
	}
	out := []profileModel{
		{Name: "claude-sonnet-5", LabelOverride: display, AnthropicFamilyTier: "sonnet"},
		{Name: "claude-opus-5", LabelOverride: display, AnthropicFamilyTier: "opus"},
		{Name: "claude-fable-5", LabelOverride: display, AnthropicFamilyTier: "fable"},
		{Name: "claude-haiku-4-5", LabelOverride: display, AnthropicFamilyTier: "haiku"},
	}
	for _, item := range settings.Catalog {
		if item.ID == "" {
			continue
		}
		model := profileModel{Name: clientcatalog.ClaudePickerID(item.ID), LabelOverride: item.ID}
		if strings.Contains(item.ID, "[1m]") {
			model.Supports1m = true
		}
		out = append(out, model)
	}
	return out
}

// TransformMeta selects profileID while preserving historical entries and
// unknown root fields.
func TransformMeta(data []byte, profileID, profileName string) ([]byte, error) {
	if profileID == "" {
		return nil, errors.New("Claude Desktop profile id is empty")
	}
	if profileName == "" {
		profileName = DefaultProfileName
	}
	if profileID == "" {
		return nil, errors.New("Claude Desktop profile id is empty")
	}
	if data != nil && len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("Claude Desktop metadata is empty")
	}
	entry, err := json.Marshal(Entry{ID: profileID, Name: profileName})
	if err != nil {
		return nil, err
	}
	applied := jsonedit.RawKV{Key: "appliedId", Value: []byte(encodeString(profileID))}
	if data == nil {
		return jsonedit.SetRootValues(nil, []jsonedit.RawKV{
			applied,
			{Key: "entries", Value: append([]byte{'['}, append(entry, ']')...)},
		})
	}
	entriesRaw, exists, err := jsonedit.RootValue(data, "entries")
	if err != nil {
		return nil, err
	}
	if exists {
		if len(bytes.TrimSpace(entriesRaw)) == 0 || entriesRaw[0] != '[' {
			return nil, errors.New("Claude Desktop metadata entries must be an array")
		}
		var entries []Entry
		if err := json.Unmarshal(entriesRaw, &entries); err != nil {
			return nil, fmt.Errorf("parse Claude Desktop metadata entries: %w", err)
		}
		found := false
		for _, item := range entries {
			if item.ID == profileID {
				found = true
				break
			}
		}
		updated, err := jsonedit.SetRootValues(data, []jsonedit.RawKV{applied})
		if err != nil {
			return nil, err
		}
		if found {
			return updated, nil
		}
		return jsonedit.AppendRootArrayValue(updated, "entries", entry)
	}
	return jsonedit.SetRootValues(data, []jsonedit.RawKV{
		applied,
		{Key: "entries", Value: append([]byte{'['}, append(entry, ']')...)},
	})
}

func NewProfileID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func TransformControl(data []byte) ([]byte, error) {
	if data != nil && len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("Claude Desktop control file is empty")
	}
	return jsonedit.SetRootStrings(data, []jsonedit.KV{{Key: DeploymentModeKey, Value: DeploymentMode3P}})
}

func CheckControl(data []byte, exists bool) (bool, error) {
	if !exists {
		return false, nil
	}
	var doc struct {
		DeploymentMode string `json:"deploymentMode"`
	}
	if err := decode(data, &doc); err != nil {
		return false, err
	}
	return doc.DeploymentMode == DeploymentMode3P, nil
}

// RestoreControl restores only deploymentMode when restoreMCP is false. When
// true, it restores the complete original control file after explicit consent.
func RestoreControl(current []byte, currentExists bool, original []byte, originalExists bool, restoreMCP bool) ([]byte, bool, error) {
	if restoreMCP {
		if originalExists {
			if err := validateObject(original); err != nil {
				return nil, false, err
			}
			return append([]byte(nil), original...), true, nil
		}
		return nil, false, nil
	}
	if !currentExists {
		return nil, false, nil
	}
	if err := validateObject(current); err != nil {
		return nil, false, err
	}
	originalValue, hasOriginal, err := jsonedit.RootValue(original, DeploymentModeKey)
	if originalExists && err != nil {
		return nil, false, err
	}
	if hasOriginal {
		updated, err := jsonedit.SetRootValues(current, []jsonedit.RawKV{{Key: DeploymentModeKey, Value: originalValue}})
		return updated, true, err
	}
	updated, err := jsonedit.RemoveRootKeys(current, DeploymentModeKey)
	if err != nil {
		return nil, false, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(updated, &object); err != nil {
		return nil, false, err
	}
	if len(object) == 0 {
		return nil, false, nil
	}
	return updated, true, nil
}

// MCPMatches compares the control-file content after removing the gateway-owned
// deploymentMode field. Formatting is irrelevant for this drift check, while
// every remaining user field participates in the comparison.
func MCPMatches(current []byte, currentExists bool, original []byte, originalExists bool) (bool, error) {
	currentCanonical, err := withoutDeploymentMode(current, currentExists)
	if err != nil {
		return false, err
	}
	originalCanonical, err := withoutDeploymentMode(original, originalExists)
	if err != nil {
		return false, err
	}
	return bytes.Equal(currentCanonical, originalCanonical), nil
}

func withoutDeploymentMode(data []byte, exists bool) ([]byte, error) {
	if !exists {
		return []byte("{}"), nil
	}
	if err := validateObject(data); err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	delete(object, DeploymentModeKey)
	return json.Marshal(object)
}

func sameGatewayURL(a, b string) bool {
	x, err1 := url.Parse(a)
	y, err2 := url.Parse(b)
	if err1 != nil || err2 != nil || x.User != nil || y.User != nil || x.RawQuery != "" || x.Fragment != "" || y.RawQuery != "" || y.Fragment != "" {
		return false
	}
	return x.Scheme == y.Scheme && x.Hostname() == "127.0.0.1" && y.Hostname() == "127.0.0.1" && x.Port() == y.Port() && x.Path == y.Path && x.Path == InferenceBasePath
}

func appliedID(data []byte) (string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return "", errors.New("Claude Desktop metadata is empty")
	}
	value, exists, err := jsonedit.RootValue(data, "appliedId")
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	var id string
	if err := json.Unmarshal(value, &id); err != nil {
		return "", fmt.Errorf("Claude Desktop metadata appliedId must be a string: %w", err)
	}
	return id, nil
}

func validateObject(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("Claude Desktop JSON is empty")
	}
	var object map[string]json.RawMessage
	if err := decode(data, &object); err != nil {
		return err
	}
	return nil
}

func decode(data []byte, target any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("Claude Desktop JSON is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("parse Claude Desktop JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("parse Claude Desktop JSON: trailing data")
		}
		return fmt.Errorf("parse Claude Desktop JSON: trailing data: %w", err)
	}
	return nil
}

func encodeString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
