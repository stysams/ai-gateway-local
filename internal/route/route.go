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
//  0. decode a Claude Code picker alias into gateway-default or
//     <provider-id>/<model-id> (identity when the string is not an alias),
//  1. read the client's current route,
//  2. an empty or gateway-default model uses the route's provider/model,
//  3. a <prefix>/<rest> model whose prefix matches a configured provider id
//     overrides the provider and strips the prefix,
//  4. for the generic client, an exact model id declared by one enabled
//     provider selects that provider; ambiguous ownership is rejected unless
//     the current route provider is one of the owners,
//  5. an exact model id declared by the current route provider is sent to
//     that provider with the full requested name — a model containing '/'
//     must never be rejected merely as an "unknown provider",
//  6. otherwise the request is rejected: the model has no attributable
//     provider, so callers must send <provider-id>/<model-id>
//     (docs/v1-scheme.md §7.4).
//
// A disabled or missing route provider only rejects requests that would
// actually use that route (steps 2 and 5). Prefix override and generic
// unique ownership must not fail with "provider <route> is disabled".
func Resolve(client ClientID, requestedModel string, cfg *config.Config) (Resolution, error) {
	requestedModel = DecodeClaudePickerID(requestedModel)
	r := RouteFor(cfg, client)
	if r.Provider == "" {
		return Resolution{}, fmt.Errorf("route for client %q is not configured", client)
	}

	if requestedModel == "" || requestedModel == ReservedModel {
		routeProvider, err := loadEnabledRouteProvider(cfg, client, r)
		if err != nil {
			return Resolution{}, err
		}
		if !modelEnabled(routeProvider, r.Model) {
			return Resolution{}, fmt.Errorf("model %q is disabled", r.Model)
		}
		return Resolution{Provider: r.Provider, Model: r.Model}, nil
	}
	if prefix, rest, ok := strings.Cut(requestedModel, "/"); ok {
		if provider, exists := cfg.Providers[prefix]; exists {
			if !provider.EnabledValue() {
				return Resolution{}, fmt.Errorf("provider %q is disabled", prefix)
			}
			if rest == "" {
				return Resolution{}, fmt.Errorf("model %q: provider prefix %q must be followed by a model name", requestedModel, prefix)
			}
			if !modelEnabled(provider, rest) {
				return Resolution{}, fmt.Errorf("model %q is disabled for provider %q", rest, prefix)
			}
			return Resolution{Provider: prefix, Model: rest}, nil
		}
	}
	if client == Generic {
		owners := modelOwners(cfg, requestedModel)
		if len(owners) == 1 {
			return Resolution{Provider: owners[0], Model: requestedModel}, nil
		}
		if len(owners) > 1 {
			if routeProvider, ok := cfg.Providers[r.Provider]; ok && routeProvider.EnabledValue() {
				for _, owner := range owners {
					if owner == r.Provider {
						return Resolution{Provider: r.Provider, Model: requestedModel}, nil
					}
				}
			}
			return Resolution{}, fmt.Errorf(
				"model %q is provided by multiple providers (%s); use <provider-id>/%s",
				requestedModel, strings.Join(owners, ", "), requestedModel,
			)
		}
	}
	routeProvider, ok := cfg.Providers[r.Provider]
	if !ok {
		return Resolution{}, fmt.Errorf("route for client %q references unknown provider %q", client, r.Provider)
	}
	if !modelListed(routeProvider, requestedModel) {
		return Resolution{}, unmatchedModelError(requestedModel)
	}
	if !routeProvider.EnabledValue() {
		return Resolution{}, fmt.Errorf("provider %q is disabled", r.Provider)
	}
	if !modelEnabled(routeProvider, requestedModel) {
		return Resolution{}, fmt.Errorf("model %q is disabled for provider %q", requestedModel, r.Provider)
	}
	return Resolution{Provider: r.Provider, Model: requestedModel}, nil
}

func loadEnabledRouteProvider(cfg *config.Config, client ClientID, r config.Route) (config.Provider, error) {
	provider, ok := cfg.Providers[r.Provider]
	if !ok {
		return config.Provider{}, fmt.Errorf("route for client %q references unknown provider %q", client, r.Provider)
	}
	if !provider.EnabledValue() {
		return config.Provider{}, fmt.Errorf("provider %q is disabled", r.Provider)
	}
	return provider, nil
}

// UnmatchedModelMessage is the data-plane error when a requested model
// cannot be attributed to any configured provider (docs/v1-scheme.md §7.4).
func UnmatchedModelMessage(requested string) string {
	return fmt.Sprintf("未匹配当前选择的[%s],请选择正确的 供应商/模型ID", requested)
}

func unmatchedModelError(requested string) error {
	return fmt.Errorf("%s", UnmatchedModelMessage(requested))
}

// modelOwners returns enabled providers that explicitly declare requested as
// their default model or as an enabled model-catalog entry. Sorting makes an
// ambiguity error stable even though providers are stored in a map.
func modelOwners(cfg *config.Config, requested string) []string {
	return cfg.ModelOwnersView(requested)
}

func modelListed(provider config.Provider, requested string) bool {
	if requested == provider.DefaultModel {
		return true
	}
	for _, model := range provider.Models {
		if model.ID == requested {
			return true
		}
	}
	return false
}

func modelDeclared(provider config.Provider, requested string) bool {
	return modelListed(provider, requested) && modelEnabled(provider, requested)
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
	// An explicit <provider-id>/<model> request may name a model the
	// provider has not published. Unprefixed names never reach this
	// fallback: Resolve rejects them when modelListed is false.
	return true
}
