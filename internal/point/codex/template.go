package codex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// LoadTemplate resolves the native catalog template used to clone routed
// entries. load, when non-nil, is the injected test source. Otherwise the
// live Codex binary is asked for its bundled catalog, then models_cache.json
// in the same home is tried. A missing template is an error; there is no
// stub prompt fallback.
func LoadTemplate(load func() ([]byte, error), cachePath string) (map[string]any, error) {
	var last error
	if load != nil {
		raw, err := load()
		if err != nil {
			return nil, fmt.Errorf("load Codex catalog template: %w", err)
		}
		return FindNativeTemplate(raw)
	}
	if raw, err := loadBundledFromCLI(); err == nil {
		if template, findErr := FindNativeTemplate(raw); findErr == nil {
			return template, nil
		} else {
			last = findErr
		}
	} else {
		last = err
	}
	if cachePath != "" {
		if raw, err := os.ReadFile(cachePath); err == nil {
			if template, findErr := FindNativeTemplate(raw); findErr == nil {
				return template, nil
			} else {
				last = findErr
			}
		} else if last == nil {
			last = err
		}
	}
	if last == nil {
		last = fmt.Errorf("no Codex catalog template source")
	}
	return nil, fmt.Errorf("codex catalog template unavailable: %w", last)
}

func loadBundledFromCLI() ([]byte, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex executable not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), CatalogLoadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "debug", "models", "--bundled")
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("codex debug models --bundled: %w: %s", err, string(exit.Stderr))
		}
		return nil, fmt.Errorf("codex debug models --bundled: %w", err)
	}
	return out, nil
}

// InvalidateModelsCache removes Codex's picker cache so the next /model read
// uses the sidecar we just wrote.
func InvalidateModelsCache(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// CatalogLoadTimeout is the budget for asking the live Codex binary for its
// bundled catalog. Kept as a named value so tests and comments agree.
const CatalogLoadTimeout = 10 * time.Second
