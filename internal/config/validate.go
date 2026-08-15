package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// providerIDRe matches docs/v1-scheme.md §5.2: ^[a-z][a-z0-9_-]{0,31}$.
var providerIDRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// validSecretRefRe matches the secret_ref charset contract shared with the
// secret package: a ref must be usable verbatim as a file name inside the
// secrets directory. The pattern is duplicated deliberately so config never
// imports secret (dependency direction, docs/v1-scheme.md §3.1); both sides
// are tested.
var validSecretRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// validAdapters is the closed set of upstream adapters.
var validAdapters = map[string]bool{
	"openai-chat":      true,
	"openai-responses": true,
	"anthropic":        true,
}

// FieldError pinpoints one invalid field. Field uses the dotted path form
// used across the management API, e.g. "providers.openrouter.base_url".
type FieldError struct {
	Field  string
	Reason string
}

func (e FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

// ValidationError aggregates every field error found in one validation pass.
// File, when set, is the config file path the errors came from.
type ValidationError struct {
	File   string
	Errors []FieldError
}

func (e *ValidationError) Error() string {
	prefix := "config invalid"
	if e.File != "" {
		prefix += " (" + e.File + ")"
	}
	msgs := make([]string, len(e.Errors))
	for i, fe := range e.Errors {
		msgs[i] = fe.Error()
	}
	return prefix + ": " + strings.Join(msgs, "; ")
}

// ValidateProvider validates one provider's id and fields without touching
// the rest of the config. The management API runs it before entering a
// secret write transaction (docs/v1-scheme.md §6.3 step 1).
func ValidateProvider(id string, p Provider) error {
	errs := validateProvider(id, p)
	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

// validateProvider collects the field errors of a single provider. It is the
// per-provider half of Config.Validate.
func validateProvider(id string, p Provider) []FieldError {
	var errs []FieldError
	if !providerIDRe.MatchString(id) {
		errs = append(errs, FieldError{
			Field:  "providers." + id,
			Reason: "id must match ^[a-z][a-z0-9_-]{0,31}$",
		})
		// The id is invalid; field paths below would be misleading.
		return errs
	}
	if strings.TrimSpace(p.Name) == "" {
		errs = append(errs, FieldError{Field: "providers." + id + ".name", Reason: "must not be empty"})
	}
	if !validAdapters[p.Adapter] {
		errs = append(errs, FieldError{
			Field:  "providers." + id + ".adapter",
			Reason: fmt.Sprintf("must be one of openai-chat, openai-responses, anthropic; got %q", p.Adapter),
		})
	}
	if !validBaseURL(p.BaseURL) {
		errs = append(errs, FieldError{
			Field:  "providers." + id + ".base_url",
			Reason: "must be an absolute http(s) URL without query string or fragment",
		})
	}
	if strings.TrimSpace(p.DefaultModel) == "" {
		errs = append(errs, FieldError{Field: "providers." + id + ".default_model", Reason: "must not be empty"})
	}
	if p.SecretRef != "" && !validSecretRefRe.MatchString(p.SecretRef) {
		errs = append(errs, FieldError{
			Field:  "providers." + id + ".secret_ref",
			Reason: "must match ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ when set",
		})
	}
	return errs
}

// Validate performs full validation of the config contract
// (docs/v1-scheme.md §5.2). It returns nil only when every rule holds.
func (c *Config) Validate() error {
	var errs []FieldError

	if c.Version != 1 {
		errs = append(errs, FieldError{Field: "version", Reason: fmt.Sprintf("must equal 1, got %d", c.Version)})
	}
	if p := c.Listen.PortValue(); p < 1024 || p > 65535 {
		errs = append(errs, FieldError{
			Field:  "listen.port",
			Reason: fmt.Sprintf("must be between 1024 and 65535, got %d", p),
		})
	}
	logDir := filepath.Clean(c.Logging.Dir)
	if logDir == "." || filepath.IsAbs(logDir) || filepath.VolumeName(logDir) != "" || logDir == ".." || strings.HasPrefix(logDir, ".."+string(filepath.Separator)) {
		errs = append(errs, FieldError{
			Field: "logging.dir", Reason: "must be a relative path inside the data root",
		})
	}

	for id, p := range c.Providers {
		errs = append(errs, validateProvider(id, p)...)
	}

	for _, name := range []string{"codex", "claude", "grok", "generic"} {
		r := c.Routes.route(name)
		if r == (Route{}) {
			errs = append(errs, FieldError{Field: "routes." + name, Reason: "must be present"})
			continue
		}
		if r.Provider == "" {
			errs = append(errs, FieldError{Field: "routes." + name + ".provider", Reason: "must reference an existing provider"})
			continue
		}
		if _, ok := c.Providers[r.Provider]; !ok {
			errs = append(errs, FieldError{
				Field:  "routes." + name + ".provider",
				Reason: fmt.Sprintf("references unknown provider %q", r.Provider),
			})
		}
		if strings.TrimSpace(r.Model) == "" {
			errs = append(errs, FieldError{Field: "routes." + name + ".model", Reason: "must not be empty"})
		}
	}

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

// validBaseURL reports whether raw is an absolute http(s) URL with no query
// string and no fragment.
func validBaseURL(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return false
	}
	return true
}
