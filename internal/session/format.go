// Package session implements export/import of browser session state
// (cookies, localStorage, sessionStorage) to/from a portable JSON bundle.
//
// The on-disk format is a single JSON document with an explicit version
// field for forward-compatibility. See FormatVersion and Bundle.
package session

import "time"

// FormatVersion is the on-disk schema version. Bump on any breaking
// change to Bundle's shape. Importers MUST refuse versions they do not
// understand rather than silently mis-interpret state.
const FormatVersion = 1

// Cookie is the portable cookie record emitted by export and consumed
// by import. Field names mirror the CDP Network.Cookie shape so that a
// human reading a bundle file can correlate it with Chrome DevTools.
//
// Expires is a Unix timestamp in seconds; -1 (or 0 with Session=true)
// indicates a session cookie with no persistent expiry.
type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Secure   bool    `json:"secure"`
	HTTPOnly bool    `json:"httpOnly"`
	SameSite string  `json:"sameSite,omitempty"`
	Expires  float64 `json:"expires"`
	Session  bool    `json:"session,omitempty"`
}

// Bundle is the top-level export document.
//
// LocalStorage and SessionStorage are keyed by origin
// (e.g. "https://store.tcgplayer.com"). IndexedDB is reserved for a
// future opt-in scope; today exporters MUST emit it as nil and
// importers MUST tolerate that.
type Bundle struct {
	Version        int                          `json:"version"`
	ExportedAt     time.Time                    `json:"exported_at"`
	BrzVersion     string                       `json:"brz_version"`
	Domains        []string                     `json:"domains"`
	Cookies        []Cookie                     `json:"cookies"`
	LocalStorage   map[string]map[string]string `json:"local_storage,omitempty"`
	SessionStorage map[string]map[string]string `json:"session_storage,omitempty"`
	IndexedDB      any                          `json:"indexed_db"`
}

// Scope identifiers used by the --include flag.
const (
	ScopeCookies        = "cookies"
	ScopeLocalStorage   = "localstorage"
	ScopeSessionStorage = "sessionstorage"
	ScopeIndexedDB      = "indexeddb"
)
