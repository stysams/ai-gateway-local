// Package route implements the client and model routing algorithm
// (docs/v1-scheme.md §7): the four fixed client ids, the reserved
// gateway-default model name and the provider-prefix override rules.
package route

import (
	"fmt"
	"strings"

	"ai-gateway/internal/config"
)

// ClientID is one of the four fixed first-class clients
// (docs/v1-scheme.md §7.1).
type ClientID string

const (
	Codex   ClientID = "codex"
	Claude  ClientID = "claude"
	Grok    ClientID = "grok"
	Generic ClientID = "generic"
)

// ReservedModel is the gateway-reserved model name meaning "use the model of
// the client's current route" (docs/v1-scheme.md §7.3).
const ReservedModel = "gateway-default"

var validClients = map[ClientID]bool{
	Codex: true, Claude: true, Grok: true, Generic: true,
}

// Valid reports whether id is one of the four fixed client ids.
func (id ClientID) Valid() bool { return validClients[id] }

// String returns the wire form of the client id.
func (id ClientID) String() string { return string(id) }

// ParseClientID validates a path client segment. Unknown clients must be
// answered with 404, never treated as generic (docs/v1-scheme.md §9.3).
func ParseClientID(s string) (ClientID, error) {
	id := ClientID(s)
	if !id.Valid() {
		return "", fmt.Errorf("unknown client %q", s)
	}
	return id, nil
}

// RouteFor returns the current route of one client (docs/v1-scheme.md §7.4
// step 1). Config validation guarantees the four routes are always present.
func RouteFor(cfg *config.Config, client ClientID) config.Route {
	switch client {
	case Codex:
		return cfg.Routes.Codex
	case Claude:
		return cfg.Routes.Claude
	case Grok:
		return cfg.Routes.Grok
	default:
		return cfg.Routes.Generic
	}
}

// Resolution is the outcome of the routing algorithm: the provider to use
// and the model name to send upstream.
type Resolution struct {
	Provider string
	Model    string
}

// Resolve implements the routing algorithm (docs/v1-scheme.md §7.4):
//
//  1. read the client's current route,
//  2. an empty or gateway-default model uses the route's provider/model,
//  3. a <prefix>/<rest> model whose prefix matches a configured provider id
//     overrides the provider and strips the prefix,
//  4. otherwise the route's provider is used with the full requested model
//     — a model containing '/' must never be rejected as "unknown
//     provider".
func Resolve(client ClientID, requestedModel string, cfg *config.Config) (Resolution, error) {
	r := RouteFor(cfg, client)
	if r.Provider == "" {
		return Resolution{}, fmt.Errorf("route for client %q is not configured", client)
	}
	routeProvider, ok := cfg.Providers[r.Provider]
	if !ok {
		return Resolution{}, fmt.Errorf("route for client %q references unknown provider %q", client, r.Provider)
	}
	if !routeProvider.EnabledValue() {
		return Resolution{}, fmt.Errorf("provider %q is disabled", r.Provider)
	}

	if requestedModel == "" || requestedModel == ReservedModel {
		if !modelEnabled(routeProvider, r.Model) {
			return Resolution{}, fmt.Errorf("model %q is disabled", r.Model)
		}
		return Resolution{Provider: r.Provider, Model: r.Model}, nil
	}
	if prefix, rest, ok := strings.Cut(requestedModel, "/"); ok {
		if _, exists := cfg.Providers[prefix]; exists {
			if !cfg.Providers[prefix].EnabledValue() {
				return Resolution{}, fmt.Errorf("provider %q is disabled", prefix)
			}
			if rest == "" {
				return Resolution{}, fmt.Errorf("model %q: provider prefix %q must be followed by a model name", requestedModel, prefix)
			}
			provider := cfg.Providers[prefix]
			if !modelEnabled(provider, rest) {
				return Resolution{}, fmt.Errorf("model %q is disabled for provider %q", rest, prefix)
			}
			return Resolution{Provider: prefix, Model: rest}, nil
		}
	}
	if !modelEnabled(routeProvider, requestedModel) {
		return Resolution{}, fmt.Errorf("model %q is disabled for provider %q", requestedModel, r.Provider)
	}
	return Resolution{Provider: r.Provider, Model: requestedModel}, nil
}

func modelEnabled(provider config.Provider, requested string) bool {
	if requested == provider.DefaultModel {
		for _, model := range provider.Models {
			if model.ID == requested {
				return model.EnabledValue()
			}
		}
		return true
	}
	for _, model := range provider.Models {
		if model.ID == requested {
			return model.EnabledValue()
		}
	}
	// A manually requested model remains a passthrough when the provider did
	// not publish an explicit entry for it.
	return true
}
