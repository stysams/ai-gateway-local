// Package clientcatalog carries the model information a point adapter writes
// into a client configuration file. It is a leaf package so the adapters can
// share the type without importing internal/point (docs/v1-scheme.md §7.3).
package clientcatalog

// ReservedModel is the provider-neutral model name every pointed client may
// use as its startup model; the gateway resolves it against the client's
// current route per request (§7.3). It must stay equal to route.ReservedModel —
// this package is a leaf so it cannot import internal/route, and
// TestReservedModelNamesAgree in internal/server guards the two against drift.
const ReservedModel = "gateway-default"

// ReservedDisplayName is the picker label for the reserved model. §7.5 makes the
// model id the display-name fallback and the §12.5 example writes it verbatim,
// so the reserved model shows as its own id.
const ReservedDisplayName = ReservedModel

// Entry is one selectable gateway model. ID is already in the
// `<provider-id>/<model-id>` form the gateway resolves per §7.4.
type Entry struct {
	ID          string
	DisplayName string
}

// Label is the name shown in a client's model picker. Client pickers always
// display the selectable id (`<provider-id>/<model-id>`), so this returns ID.
func (e Entry) Label() string {
	if e.ID != "" {
		return e.ID
	}
	return e.DisplayName
}

// Settings is what a client configuration must express after point.
//
// PreferredModel is the client's startup model only; per §7.3 it never limits
// which models the user can pick inside the agent. Catalog is the complete set
// of enabled models and is only written for clients that can hold a list
// natively — see the 2026-08-15 verification record in §20.
// RemoteCompaction is Codex-only: Claude and Grok ignore it.
type Settings struct {
	PreferredModel   string
	Catalog          []Entry
	RemoteCompaction bool
}

// Model returns the model name a client should start with. An unset preferred
// model means "follow the route", which is exactly what ReservedModel encodes.
func (s Settings) Model() string {
	if s.PreferredModel != "" {
		return s.PreferredModel
	}
	return ReservedModel
}

// Equal reports whether two settings would produce the same client configuration.
func (s Settings) Equal(other Settings) bool {
	if s.PreferredModel != other.PreferredModel || s.RemoteCompaction != other.RemoteCompaction || len(s.Catalog) != len(other.Catalog) {
		return false
	}
	for i, entry := range s.Catalog {
		if entry != other.Catalog[i] {
			return false
		}
	}
	return true
}
