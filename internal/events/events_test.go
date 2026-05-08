package events

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJSONLMonotonicSeq(t *testing.T) {
	var buf bytes.Buffer
	em := NewJSONL(&buf)
	em.now = func() time.Time { return time.Unix(1700000000, 123456789).UTC() }

	em.Emit(Event{Event: WorkflowStart, Workflow: "wf"})
	em.Emit(Event{Event: StepStart, Step: "s1"})
	em.Emit(Event{Event: StepEnd, Step: "s1", Status: StatusOK})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), buf.String())
	}

	var prev uint64
	for i, line := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d not valid JSON: %v: %q", i, err, line)
		}
		if ev.Seq != prev+1 {
			t.Errorf("line %d: seq=%d want %d", i, ev.Seq, prev+1)
		}
		prev = ev.Seq
		if ev.Timestamp == "" {
			t.Errorf("line %d: empty ts", i)
		}
		if _, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err != nil {
			t.Errorf("line %d: ts not RFC3339Nano: %v", i, err)
		}
	}
}

func TestJSONLConcurrentEmit(t *testing.T) {
	var buf bytes.Buffer
	em := NewJSONL(&buf)

	const N = 100
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			em.Emit(Event{Event: StepStart})
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != N {
		t.Fatalf("expected %d lines, got %d", N, len(lines))
	}
	seen := make(map[uint64]bool, N)
	for _, line := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid JSON line: %v: %q", err, line)
		}
		if ev.Seq < 1 || ev.Seq > N {
			t.Errorf("seq out of range: %d", ev.Seq)
		}
		if seen[ev.Seq] {
			t.Errorf("duplicate seq: %d", ev.Seq)
		}
		seen[ev.Seq] = true
	}
}

func TestNopEmitter(t *testing.T) {
	var n Nop
	// Should not panic and should not write anywhere.
	n.Emit(Event{Event: WorkflowStart})
}

func TestJSONLOmitsZeroFields(t *testing.T) {
	var buf bytes.Buffer
	em := NewJSONL(&buf)
	em.Emit(Event{Event: WorkflowStart})

	line := strings.TrimRight(buf.String(), "\n")
	// Spot-check: duration_ms zero must be omitted.
	if strings.Contains(line, "duration_ms") {
		t.Errorf("zero duration_ms should be omitted: %s", line)
	}
	if !strings.Contains(line, `"event":"workflow_start"`) {
		t.Errorf("missing event field: %s", line)
	}
}
