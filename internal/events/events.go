// Package events provides a structured JSONL event stream for brz workflow runs.
//
// An Emitter is a write-only sink for lifecycle events. The default Nop
// emitter discards everything; the JSONL emitter encodes one JSON object
// per line to an io.Writer (typically os.Stdout when --events=jsonl).
//
// Threading: the JSONL emitter holds an internal mutex around the seq
// counter, timestamp stamping, and encoder write. The emitter never calls
// back into caller code, so callers may hold their own lock while invoking
// Emit (the executor in fact does — it emits while holding e.mu). The
// only constraint is that user-supplied Emitter implementations must not
// re-enter the executor or any caller-held lock.
package events

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Event is the payload encoded as one JSONL line. Fields are intentionally
// flat and stable — agents stream-parse this.
//
// `Event` and `Status` are required for every record; everything else is
// omitempty so a step_start doesn't carry zero-valued duration_ms etc.
type Event struct {
	Timestamp        string                 `json:"ts"`
	Seq              uint64                 `json:"seq"`
	Event            string                 `json:"event"`
	Workflow         string                 `json:"workflow,omitempty"`
	Action           string                 `json:"action,omitempty"`
	Step             string                 `json:"step,omitempty"`
	StepNum          int                    `json:"step_num,omitempty"`
	StepType         string                 `json:"step_type,omitempty"`
	Target           string                 `json:"target,omitempty"`
	Value            string                 `json:"value,omitempty"`
	Status           string                 `json:"status,omitempty"`
	DurationMs       int64                  `json:"duration_ms,omitempty"`
	Error            string                 `json:"error,omitempty"`
	Attempt          int                    `json:"attempt,omitempty"`
	RetriesRemaining int                    `json:"retries_remaining,omitempty"`
	Path             string                 `json:"path,omitempty"`
	Size             int64                  `json:"size,omitempty"`
	URL              string                 `json:"url,omitempty"`
	StepsTotal       int                    `json:"steps_total,omitempty"`
	StepsFailed      int                    `json:"steps_failed,omitempty"`
	Result           interface{}            `json:"result,omitempty"`
	Extra            map[string]interface{} `json:"extra,omitempty"`
}

// Event names. Centralised so callers don't spell them differently.
const (
	WorkflowStart      = "workflow_start"
	WorkflowEnd        = "workflow_end"
	ActionStart        = "action_start"
	ActionEnd          = "action_end"
	StepStart          = "step_start"
	StepEnd            = "step_end"
	RetryAttempt       = "retry_attempt"
	DownloadStarted    = "download_started"
	DownloadCompleted  = "download_completed"
	ScreenshotCaptured = "screenshot_captured"
	EvalResult         = "eval_result"
)

// Status values for *_end events.
const (
	StatusOK      = "ok"
	StatusError   = "error"
	StatusSkipped = "skipped"
)

// Emitter accepts events. Implementations must be safe for concurrent use.
type Emitter interface {
	Emit(e Event)
}

// Nop discards every event. The zero value is ready to use.
type Nop struct{}

// Emit implements Emitter. It does nothing.
func (Nop) Emit(Event) {}

// JSONL writes one JSON object per line to w. Concurrency-safe: a single
// mutex serialises seq generation, timestamp stamping, and encoder writes,
// guaranteeing the on-wire ordering matches seq order. Without holding the
// lock across all three steps, two goroutines could grab seq=1 and seq=2
// then write in reverse order.
type JSONL struct {
	mu  sync.Mutex
	enc *json.Encoder
	seq uint64
	now func() time.Time // injectable for tests
}

// NewJSONL returns a JSONL emitter writing to w.
func NewJSONL(w io.Writer) *JSONL {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &JSONL{enc: enc, now: time.Now}
}

// Emit implements Emitter. It stamps Timestamp and Seq if unset.
//
// seq is assigned under the same lock that wraps the write, so on-wire
// order matches seq order even under concurrent emit.
func (j *JSONL) Emit(e Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if e.Seq == 0 {
		j.seq++
		e.Seq = j.seq
	}
	if e.Timestamp == "" {
		e.Timestamp = j.now().UTC().Format(time.RFC3339Nano)
	}
	// json.Encoder.Encode appends a newline, so output is valid JSONL.
	// Errors are intentionally swallowed: stdout is best-effort, and a
	// broken pipe shouldn't crash a workflow (matches conventions of
	// other streaming CLIs like jq, head).
	_ = j.enc.Encode(&e)
}
