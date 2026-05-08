package workflow

import (
	"strings"
	"time"

	"github.com/crackfetch/brainstorm/internal/baseline"
	"github.com/go-rod/rod"
)

// StepObserver is invoked once per step during action execution. Its job
// is to record per-step signals for site-drift detection — matched_count
// and the first match's text fingerprint, when the step has a selector.
//
// The observer is called even when the step itself didn't perform a
// selector lookup (e.g. navigate, sleep, eval) so the recorded step list
// stays aligned 1:1 with the action's Steps.
//
// All hooks are best-effort: errors fetching matches are swallowed and
// recorded as MatchedCount=nil. The drift check is purely informational
// (or terminal under --strict-drift in the CLI), so it must never break
// a working run.
type StepObserver interface {
	// OnStep is called after the step has executed (before retry/optional
	// handling). idx is 0-indexed. ok reflects step success.
	OnStep(idx int, step Step, action string, ok bool, page *rod.Page)
}

// RecordingObserver collects baseline.StepObservation values and emits
// drift events to a callback when a comparator is provided.
type RecordingObserver struct {
	Comparator *baseline.Comparator
	OnDrift    func(baseline.Drift)
	steps      []baseline.StepObservation
}

// NewRecordingObserver builds an observer ready to record steps. If cmp
// is non-nil, drifts are pushed to onDrift as they're discovered.
func NewRecordingObserver(cmp *baseline.Comparator, onDrift func(baseline.Drift)) *RecordingObserver {
	return &RecordingObserver{Comparator: cmp, OnDrift: onDrift}
}

// Steps returns the recorded observations. The slice is owned by the
// observer; callers should not mutate it.
func (o *RecordingObserver) Steps() []baseline.StepObservation {
	return o.steps
}

// OnStep records a single observation, probing the page for selector
// matches when the step has a selector. idx is the per-action step index
// (0-based) — the recorder maintains its own monotonic global index so
// multi-action runs stay aligned with the on-disk baseline.
func (o *RecordingObserver) OnStep(idx int, step Step, action string, ok bool, page *rod.Page) {
	if o == nil {
		return
	}
	globalIdx := len(o.steps)
	obs := baseline.StepObservation{
		Name:   stepLabel(step, idx),
		Action: action,
		OK:     ok,
	}
	if step.Navigate != "" {
		obs.Target = step.Navigate
	}

	selector := StepSelector(step)
	if selector != "" && page != nil {
		obs.Selector = selector
		// Bound the probe in time. We're sampling current page state, not
		// waiting for elements to appear (the step itself already did
		// whatever waiting was configured). 2s is generous for a DOM
		// query but won't hang the run if the page is broken.
		probePage := page.Timeout(2 * time.Second)
		if els, err := probePage.Elements(selector); err == nil {
			n := len(els)
			obs.MatchedCount = baseline.IntPtr(n)
			if n > 0 {
				if text, terr := els[0].Text(); terr == nil && strings.TrimSpace(text) != "" {
					obs.SampleTextHash = baseline.HashText(text)
				}
			}
		}
	}

	o.steps = append(o.steps, obs)

	// Drift compare — only when we have a measurement to compare. Use the
	// global index so a baseline written from a multi-action run aligns
	// step-for-step on replay.
	if o.Comparator != nil && obs.MatchedCount != nil && o.OnDrift != nil {
		baseObs, found := o.Comparator.ObservationFor(globalIdx, selector)
		if found {
			for _, d := range o.Comparator.Compare(baseObs, obs) {
				o.OnDrift(d)
			}
		}
	}
}

func stepLabel(step Step, idx int) string {
	if step.Label != "" {
		return step.Label
	}
	return defaultStepName(idx)
}

func defaultStepName(idx int) string {
	// 1-indexed for human readability ("step 1", "step 2", ...).
	return "step " + itoa(idx+1)
}

// itoa avoids strconv just to keep this file's imports minimal — we
// already have strings, and these step names are short.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// WithObserver attaches a StepObserver to the executor.
func WithObserver(o StepObserver) Option {
	return func(e *Executor) { e.observer = o }
}
