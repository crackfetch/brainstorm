package workflow

// Option configures an Executor.
type Option func(*Executor)

// WithHeaded shows the browser window (useful for CAPTCHAs or debugging).
func WithHeaded(b bool) Option {
	return func(e *Executor) { e.headed = b }
}

// WithDebug enables verbose logging and failure screenshots.
func WithDebug(b bool) Option {
	return func(e *Executor) { e.debug = b }
}

// WithProfileDir sets the Chrome profile directory for session persistence.
func WithProfileDir(dir string) Option {
	return func(e *Executor) { e.profileDir = dir }
}
