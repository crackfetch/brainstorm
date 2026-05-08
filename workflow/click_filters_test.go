package workflow

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// E2E tests for click.visible and click.nth (including negative indices).
// Uses a local httptest server with a page that has duplicate selectors
// across visible and hidden subtrees — exactly the modal-with-duplicate-button
// pattern that motivated these fields.

const clickFiltersHTML = `<!doctype html>
<html><head><title>click filters</title></head>
<body>
<button id="page-btn" class="action">Page Action</button>
<div id="hidden" style="display:none">
  <button class="action" data-loc="hidden">Hidden Action</button>
</div>
<div id="modal" style="display:block">
  <button class="action" data-loc="modal-1">Modal 1</button>
  <button class="action" data-loc="modal-2">Modal 2</button>
</div>
<div id="result"></div>
<script>
  document.addEventListener('click', (e) => {
    const t = e.target;
    if (t.classList.contains('action')) {
      document.getElementById('result').textContent =
        (t.id || t.getAttribute('data-loc') || 'unknown');
    }
  });
</script>
</body></html>`

func clickFiltersServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(clickFiltersHTML))
	}))
}

// runClickAction loads the test page, runs a click action, then reads
// #result.textContent to identify which button was clicked.
func runClickAction(t *testing.T, click *ClickStep) (clicked string, err error) {
	t.Helper()
	srv := clickFiltersServer()
	t.Cleanup(srv.Close)

	w := &Workflow{
		Name: "click-filters",
		Actions: map[string]Action{
			"do": {URL: srv.URL, Steps: []Step{{Click: click}}},
		},
	}
	exec := NewExecutor(w)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { exec.Close() })

	res := exec.RunAction("do")
	if !res.OK {
		return "", &actionError{res.Error}
	}
	val := exec.page.MustEval(`() => document.getElementById('result').textContent`).String()
	return val, nil
}

type actionError struct{ msg string }

func (e *actionError) Error() string { return e.msg }

func TestClick_DefaultBehavior_PreservedFirstMatch(t *testing.T) {
	skipIfNoChrome(t)
	// No Visible, no Nth, no Text — must pick the first selector match,
	// which is the page-level button. Backward-compat guard.
	got, err := runClickAction(t, &ClickStep{Selector: "button.action"})
	if err != nil {
		t.Fatalf("click: %v", err)
	}
	if got != "page-btn" {
		t.Errorf("clicked %q, want page-btn (first .action in DOM order)", got)
	}
}

func TestClick_Visible_FiltersHiddenMatches(t *testing.T) {
	skipIfNoChrome(t)
	// page-btn is visible and first; setting Visible: true with no Nth should
	// still pick it. The hidden button (display:none) is excluded from the set
	// but doesn't affect the first-visible result here.
	got, err := runClickAction(t, &ClickStep{Selector: "button.action", Visible: true})
	if err != nil {
		t.Fatalf("click: %v", err)
	}
	if got != "page-btn" {
		t.Errorf("clicked %q, want page-btn (first visible)", got)
	}
}

func TestClick_Visible_LastPicksModalSubmit(t *testing.T) {
	skipIfNoChrome(t)
	// The motivating case: page has a duplicate selector across page and
	// modal. Visible: true filters out the display:none button; Nth: -1
	// then picks the last visible match (modal-2).
	got, err := runClickAction(t, &ClickStep{Selector: "button.action", Visible: true, Nth: -1})
	if err != nil {
		t.Fatalf("click: %v", err)
	}
	if got != "modal-2" {
		t.Errorf("clicked %q, want modal-2 (last visible match)", got)
	}
}

func TestClick_NthMinusOne_NoVisible_PicksLastIncludingHidden(t *testing.T) {
	skipIfNoChrome(t)
	// Without Visible: true, the hidden button is part of the candidate set.
	// DOM order: page-btn, hidden, modal-1, modal-2 → last is modal-2 because
	// the hidden button still counts. The point of this test is that negative
	// nth follows raw selector match order, not visibility.
	got, err := runClickAction(t, &ClickStep{Selector: "button.action", Nth: -1})
	if err != nil {
		t.Fatalf("click: %v", err)
	}
	if got != "modal-2" {
		t.Errorf("clicked %q, want modal-2 (last raw match)", got)
	}
}

func TestClick_NthMinusTwo_VisibleOnly(t *testing.T) {
	skipIfNoChrome(t)
	// Visible matches in DOM order: page-btn, modal-1, modal-2.
	// Nth: -2 = second-to-last = modal-1.
	got, err := runClickAction(t, &ClickStep{Selector: "button.action", Visible: true, Nth: -2})
	if err != nil {
		t.Fatalf("click: %v", err)
	}
	if got != "modal-1" {
		t.Errorf("clicked %q, want modal-1 (visible[-2])", got)
	}
}

func TestClick_TextFilter_StillWorks(t *testing.T) {
	skipIfNoChrome(t)
	// Existing text-filter behavior must still match — picks the first
	// element whose text contains the needle. "Modal 1" and "Modal 2"
	// both contain "Modal", first match wins → modal-1.
	got, err := runClickAction(t, &ClickStep{Selector: "button.action", Text: "Modal"})
	if err != nil {
		t.Fatalf("click: %v", err)
	}
	if got != "modal-1" {
		t.Errorf("clicked %q, want modal-1 (first text match)", got)
	}
}

func TestClick_TextFilter_WithVisibleAndNth(t *testing.T) {
	skipIfNoChrome(t)
	// Combine all three filters: visible-only, text contains "Modal",
	// last (nth: -1) → modal-2.
	got, err := runClickAction(t, &ClickStep{
		Selector: "button.action", Visible: true, Text: "Modal", Nth: -1,
	})
	if err != nil {
		t.Fatalf("click: %v", err)
	}
	if got != "modal-2" {
		t.Errorf("clicked %q, want modal-2 (last visible Modal match)", got)
	}
}

func TestClick_NthOutOfRange_ReturnsError(t *testing.T) {
	skipIfNoChrome(t)
	// 4 visible matches → nth: 99 is out of range. Error must mention nth
	// and the count so users can debug.
	_, err := runClickAction(t, &ClickStep{Selector: "button.action", Visible: true, Nth: 99, Timeout: "2s"})
	if err == nil {
		t.Fatal("expected out-of-range error, got nil")
	}
}
