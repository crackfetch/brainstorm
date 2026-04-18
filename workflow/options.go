package workflow

// Option configures an Executor.
type Option func(*Executor)

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
// "store.tcgplayer.com/admin").
func WithLoginURL(loginURL, successURL string) Option {
	return func(e *Executor) {
		e.loginURL = loginURL
		e.loginSuccessURL = successURL
	}
}
