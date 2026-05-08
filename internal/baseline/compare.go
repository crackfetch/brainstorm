package baseline

import (
	"fmt"
)

// DriftKind enumerates the drift signals brz currently emits.
type DriftKind string

const (
	// DriftSelectorCountChanged: matched_count differs from baseline,
	// but both sides are >0. Surfaces "12 → 13" style drifts where the
	// selector still matches something but the count changed.
	DriftSelectorCountChanged DriftKind = "selector_count_changed"
	// DriftSelectorNoLongerMatches: baseline had >0 matches, current run
	// has 0. Strongest signal that the page changed under the selector.
	DriftSelectorNoLongerMatches DriftKind = "selector_no_longer_matches"
	// DriftTextPatternChanged: same matched_count, but the first match's
	// trimmed-lowercased innerText hash differs. Cheap "page looks
	// completely different" fingerprint.
	DriftTextPatternChanged DriftKind = "text_pattern_changed"
)

// Drift describes a single drift event for one step.
type Drift struct {
	Kind         DriftKind `json:"kind"`
	StepName     string    `json:"step_name"`
	Action       string    `json:"action,omitempty"`
	Selector     string    `json:"selector,omitempty"`
	BaselineHits int       `json:"baseline_hits"`
	CurrentHits  int       `json:"current_hits"`
	BaselineHash string    `json:"baseline_hash,omitempty"`
	CurrentHash  string    `json:"current_hash,omitempty"`
}

// String returns a human-readable one-line description.
func (d Drift) String() string {
	switch d.Kind {
	case DriftSelectorNoLongerMatches:
		return fmt.Sprintf("DRIFT %s: step %q selector %q matched %d before, 0 now",
			d.Kind, d.StepName, d.Selector, d.BaselineHits)
	case DriftSelectorCountChanged:
		return fmt.Sprintf("DRIFT %s: step %q selector %q matched %d before, %d now",
			d.Kind, d.StepName, d.Selector, d.BaselineHits, d.CurrentHits)
	case DriftTextPatternChanged:
		return fmt.Sprintf("DRIFT %s: step %q selector %q first-match text fingerprint changed (%s → %s)",
			d.Kind, d.StepName, d.Selector, shortHash(d.BaselineHash), shortHash(d.CurrentHash))
	default:
		return fmt.Sprintf("DRIFT %s: step %q selector %q", d.Kind, d.StepName, d.Selector)
	}
}

func shortHash(h string) string {
	const prefix = "sha256:"
	if len(h) > len(prefix)+8 {
		return h[:len(prefix)+8]
	}
	return h
}

// Comparator compares live observations against a baseline. Look up the
// baseline observation for a given step index via ObservationFor.
type Comparator struct {
	baseline *Baseline
}

// NewComparator builds a comparator around a baseline. If baseline is nil,
// all comparisons return no drift (so callers can use a comparator
// unconditionally).
func NewComparator(b *Baseline) *Comparator {
	return &Comparator{baseline: b}
}

// Compare returns the drift between a baseline observation and a live one.
// If they are nil-equal or compatible, the returned slice is empty.
func (c *Comparator) Compare(baseObs, liveObs StepObservation) []Drift {
	if c == nil || c.baseline == nil {
		return nil
	}
	// No baseline measurement to compare against.
	if baseObs.MatchedCount == nil || liveObs.MatchedCount == nil {
		return nil
	}
	bHits := *baseObs.MatchedCount
	lHits := *liveObs.MatchedCount
	var drifts []Drift

	switch {
	case bHits > 0 && lHits == 0:
		drifts = append(drifts, Drift{
			Kind:         DriftSelectorNoLongerMatches,
			StepName:     liveObs.Name,
			Action:       liveObs.Action,
			Selector:     liveObs.Selector,
			BaselineHits: bHits,
			CurrentHits:  lHits,
		})
	case bHits != lHits:
		drifts = append(drifts, Drift{
			Kind:         DriftSelectorCountChanged,
			StepName:     liveObs.Name,
			Action:       liveObs.Action,
			Selector:     liveObs.Selector,
			BaselineHits: bHits,
			CurrentHits:  lHits,
		})
	}

	// Text fingerprint: only compare when both sides have a hash. Don't
	// fire if either is empty (an empty innerText is uninformative).
	if baseObs.SampleTextHash != "" && liveObs.SampleTextHash != "" &&
		baseObs.SampleTextHash != liveObs.SampleTextHash {
		drifts = append(drifts, Drift{
			Kind:         DriftTextPatternChanged,
			StepName:     liveObs.Name,
			Action:       liveObs.Action,
			Selector:     liveObs.Selector,
			BaselineHits: bHits,
			CurrentHits:  lHits,
			BaselineHash: baseObs.SampleTextHash,
			CurrentHash:  liveObs.SampleTextHash,
		})
	}

	return drifts
}

// ObservationFor returns the baseline observation matching the given step
// index + selector. Returns false if the baseline doesn't have a matching
// step at that index, or if the selector differs (workflow edited since
// baseline capture, so silently skip rather than warn).
func (c *Comparator) ObservationFor(idx int, selector string) (StepObservation, bool) {
	if c == nil || c.baseline == nil {
		return StepObservation{}, false
	}
	if idx < 0 || idx >= len(c.baseline.Steps) {
		return StepObservation{}, false
	}
	obs := c.baseline.Steps[idx]
	if selector != "" && obs.Selector != "" && obs.Selector != selector {
		return StepObservation{}, false
	}
	return obs, true
}
