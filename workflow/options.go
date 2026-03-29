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
