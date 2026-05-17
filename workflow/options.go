package workflow

import "github.com/crackfetch/brainstorm/internal/events"

// Option configures an Executor.
type Option func(*Executor)

// WithEventEmitter installs an events.Emitter on the executor. The executor
// will publish step_start / step_end / retry_attempt / download_* events to
// this sink as actions run. Default emitter is events.Nop (zero overhead).
//
// The emitter is invoked synchronously while e.mu is held. The shipped
// events.JSONL emitter only writes to its own io.Writer, so this is safe.
// User-supplied implementations MUST NOT (a) call back into Executor methods
// — that would re-lock e.mu and deadlock — and MUST NOT (b) block on I/O
// that could stall every executor method. Treat Emit as fire-and-forget.
func WithEventEmitter(em events.Emitter) Option {
	return func(e *Executor) {
		if em != nil {
			e.events = em
		}
	}
}

// WithHeaded shows the browser window (useful for CAPTCHAs or debugging).
func WithHeaded(b bool) Option {
	return func(e *Executor) { e.headed = b }
}

// WithAutoHeaded enables auto-escalation: start headless, but if an action
// marked headed:true fails, relaunch in headed mode and retry.
func WithAutoHeaded(b bool) Option {
	return func(e *Executor) { e.autoHeaded = b }
}

// WithDebug enables verbose logging and failure screenshots.
func WithDebug(b bool) Option {
	return func(e *Executor) { e.debug = b }
}

// WithProfileDir sets the Chrome profile directory for session persistence.
func WithProfileDir(dir string) Option {
	return func(e *Executor) { e.profileDir = dir }
}

// WithLoginURL configures delayed CDP connection. When set, Start() launches
// Chrome headed with this URL but does NOT connect CDP — this avoids bot
// detection on login pages that check for active DevTools connections. After
// the user logs in manually, call ConnectAfterLogin() to establish CDP.
// successURL is a substring the browser URL must contain after login (e.g.
// "example.com/dashboard").
func WithLoginURL(loginURL, successURL string) Option {
	return func(e *Executor) {
		e.loginURL = loginURL
		e.loginSuccessURL = successURL
	}
}

// WithChromeFlags forwards extra Chrome command-line flags to the launcher.
// Each map entry becomes a "--name=value" arg, or a bare "--name" when the
// value is the empty string. Applied AFTER the built-in stealth/viewport
// flags so callers can override brainstorm defaults if they need to (e.g.
// pin a different --window-position, or disable Chrome's background-tab
// throttling for long-running automation that runs while the window is
// minimized or occluded).
//
// Multiple WithChromeFlags calls accumulate. A later call's keys overwrite
// earlier ones for the same name. Passing nil or an empty map is a no-op.
// Backwards compatible: omitting this option produces identical launcher
// args to prior versions.
func WithChromeFlags(flags map[string]string) Option {
	return func(e *Executor) {
		if len(flags) == 0 {
			return
		}
		if e.chromeFlags == nil {
			e.chromeFlags = make(map[string]string, len(flags))
		}
		for k, v := range flags {
			e.chromeFlags[k] = v
		}
	}
}

