package server

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"ai-gateway/internal/autostart"
	"ai-gateway/internal/config"
	"ai-gateway/internal/logstore"
	"ai-gateway/internal/point"
	"ai-gateway/internal/secret"
)

// DoctorReport is the GET /api/v1/doctor payload.
type DoctorReport struct {
	Config      ConfigCheck             `json:"config"`
	SecretStore SecretStoreCheck        `json:"secret_store"`
	Secrets     SecretsCheck            `json:"secrets"`
	Logs        logstore.Inspection     `json:"logs"`
	Clients     map[string]point.Status `json:"clients"`
	Autostart   AutostartCheck          `json:"autostart"`
}

// AutostartCheck compares config with the live per-user registration. A task
// pointing at an old installation path is an explicit doctor failure.
type AutostartCheck struct {
	OK            bool                   `json:"ok"`
	ConfigEnabled bool                   `json:"config_enabled"`
	Registration  autostart.Registration `json:"registration"`
	Error         string                 `json:"error,omitempty"`
}

// ConfigCheck reports whether the loaded config is present and valid.
type ConfigCheck struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// SecretStoreCheck reports whether the platform system key store is usable.
type SecretStoreCheck struct {
	OK       bool   `json:"ok"`
	Platform string `json:"platform,omitempty"`
	Error    string `json:"error,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// SecretsCheck reports the required secrets and any anomalies: missing
// secrets (required but unreadable) and orphan secrets (present in the store
// but referenced by no provider, docs/v1-scheme.md §6.3).
type SecretsCheck struct {
	Required int      `json:"required"`
	Missing  []string `json:"missing,omitempty"`
	Orphans  []string `json:"orphans,omitempty"`
	OK       bool     `json:"ok"`
}

// handleDoctor always answers 200 with the report: doctor is a diagnostic
// surface, not a gate (docs/v1-scheme.md §13.4).
func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.doctorReport(r.Context()))
}

// doctorReport assembles the B-scope checks.
func (s *Server) doctorReport(ctx context.Context) DoctorReport {
	var report DoctorReport

	cfg := s.cfg.View()
	configOK := false
	if cfg == nil {
		report.Config = ConfigCheck{OK: false, Error: "config not loaded"}
	} else if err := cfg.Validate(); err != nil {
		report.Config = ConfigCheck{OK: false, Error: err.Error()}
	} else {
		configOK = true
		report.Config = ConfigCheck{OK: true}
	}

	if err := s.secrets.Available(ctx); err != nil {
		report.SecretStore = SecretStoreCheck{OK: false, Error: err.Error()}
		if errors.Is(err, secret.ErrUnavailable) {
			report.SecretStore.Hint = "ai-gateway never falls back to plaintext storage; configure the platform key store, or remove the providers that require keys"
		}
	} else {
		report.SecretStore = SecretStoreCheck{OK: true}
	}
	if p, ok := s.secrets.(secret.Platformer); ok {
		report.SecretStore.Platform = p.Platform()
	}

	if configOK {
		report.Secrets = s.secretsCheck(ctx, cfg)
		report.Logs = s.warnings.Inspect(cfg.Logging.Dir, cfg.Logging.EnabledValue())
		baseURL := s.ClientBaseURL(cfg)
		report.Clients = map[string]point.Status{
			"codex":  s.points.Check(point.ClientCodex, baseURL, s.clientSettings(cfg, point.ClientCodex)),
			"claude": s.points.Check(point.ClientClaude, baseURL, s.clientSettings(cfg, point.ClientClaude)),
			"grok":   s.points.Check(point.ClientGrok, baseURL, s.clientSettings(cfg, point.ClientGrok)),
		}
		report.Autostart.ConfigEnabled = cfg.Autostart.Enabled
		registration, err := s.autostart.Status()
		report.Autostart.Registration = registration
		switch {
		case err != nil:
			report.Autostart.Error = err.Error()
		case cfg.Autostart.Enabled != registration.Enabled:
			report.Autostart.Error = "config and operating-system login registration disagree"
		case !cfg.Autostart.Enabled && registration.Exists:
			report.Autostart.Error = "a disabled or stale operating-system login registration still exists"
		case registration.Enabled && !registration.Valid:
			report.Autostart.Error = registration.Issue
		default:
			report.Autostart.OK = true
		}
	}
	// With an invalid config the required-secret set is unknowable; Secrets
	// stays at its zero value (OK=false, nothing claimed).
	return report
}

// secretsCheck enumerates missing and orphan secrets.
func (s *Server) secretsCheck(ctx context.Context, cfg *config.Config) SecretsCheck {
	var check SecretsCheck
	used := make(map[string]string, len(cfg.Providers)) // ref -> provider id
	for id, p := range cfg.Providers {
		if p.SecretRef == "" {
			continue
		}
		check.Required++
		used[p.SecretRef] = id
		b, err := s.secrets.Get(ctx, p.SecretRef)
		if b != nil {
			secret.Zero(b)
		}
		if err != nil {
			check.Missing = append(check.Missing, id+" (ref "+p.SecretRef+")")
		}
	}

	if lister, ok := s.secrets.(secret.Lister); ok {
		refs, err := lister.List(ctx)
		if err == nil {
			for _, ref := range refs {
				if _, referenced := used[ref]; !referenced {
					check.Orphans = append(check.Orphans, ref)
				}
			}
		}
	}

	sort.Strings(check.Missing)
	sort.Strings(check.Orphans)
	check.OK = len(check.Missing) == 0
	return check
}
