package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/store"
)

// VersionCacheTTL is how long an API node reuses the node list and schema version (OC-058).
const VersionCacheTTL = 10 * time.Second

// VersionInfo is GET /api/v1/version for a signed-in caller.
type VersionInfo struct {
	buildinfo.Info
	MetadataSchemaVersion int                 `json:"metadataSchemaVersion"`
	Nodes                 map[string][]string `json:"nodes"`
	Consistent            bool                `json:"consistent"`
	// BundledStore names the Couchbase node bundled in the all-in-one image (OC-072).
	BundledStore string `json:"bundledStore,omitempty"`
}

// VersionHandler serves GET /api/v1/version by audience (OC-058): a signed-in caller gets the
// full build, the metadata schema version and the versions of the running nodes; an anonymous
// caller only {version, channel} (enough for the sign-in page), or {} when Expose is false.
type VersionHandler struct {
	Info buildinfo.Info
	// Expose shows the version and channel to anonymous callers (OC_EXPOSE_BUILD_INFO).
	Expose bool
	// Authenticated reports whether the request carries a session (the session middleware ran).
	Authenticated func(*http.Request) bool
	Nodes         store.Nodes                        // nil: only this node is reported
	Schema        func(context.Context) (int, error) // nil: 0
	Role          string                             // this node's OC_ROLE, reported when Nodes is nil
	BundledStore  string                             // OC_BUNDLED_STORE (OC-072)
	Logger        *slog.Logger
	Now           func() time.Time

	mu      sync.Mutex
	cached  *VersionInfo
	expires time.Time
}

func (h *VersionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Authenticated == nil || !h.Authenticated(r) {
		if !h.Expose {
			writeJSON(w, http.StatusOK, struct{}{})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"version": h.Info.Version, "channel": h.Info.Channel})
		return
	}
	writeJSON(w, http.StatusOK, h.full(r.Context()))
}

func (h *VersionHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// full builds the signed-in view, cached for VersionCacheTTL. A metadata-store error degrades
// to this node alone rather than failing: the footer must render even when Capella is slow.
func (h *VersionHandler) full(ctx context.Context) VersionInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if h.cached != nil && now.Before(h.expires) {
		return *h.cached
	}
	v := VersionInfo{Info: h.Info, BundledStore: h.BundledStore}
	role := h.Role
	if role == "" {
		role = store.NodeRoleAll
	}
	beats := []store.NodeBeat{{ID: "self", Role: role, Version: h.Info.Version}}
	if h.Nodes != nil {
		live, err := h.Nodes.Live(ctx)
		switch {
		case err != nil:
			h.log().Warn("version: listing nodes failed", "error", err.Error())
		case len(live) > 0:
			beats = live
		}
	}
	v.Nodes, v.Consistent = store.VersionsByRole(beats)
	if h.Schema != nil {
		n, err := h.Schema(ctx)
		if err != nil {
			h.log().Warn("version: reading the metadata schema version failed", "error", err.Error())
		}
		v.MetadataSchemaVersion = n
	}
	h.cached, h.expires = &v, now.Add(VersionCacheTTL)
	return v
}

func (h *VersionHandler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}
