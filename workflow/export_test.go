package workflow

import "io"

// SetAnnounceWriterForTest overrides the writer used for the headed-launch
// announcement. Test-only entry point — exists in this _test.go file so it
// is not part of the public package API. Production callers cannot install
// custom writers (which could deadlock the executor if they block while
// e.mu is held during launch).
func (e *Executor) SetAnnounceWriterForTest(w io.Writer) {
	e.announceWriter = w
}
