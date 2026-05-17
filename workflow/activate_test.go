package workflow

import "testing"

// TestActivatePage_NoPageIsNoop pins the documented contract: calling
// ActivatePage before any page is attached returns nil rather than
// panicking or returning an error. Lets callers invoke it defensively
// (e.g. behind an env-gated branch) without first reaching into
// Executor internals to check page state.
//
// The CDP-against-real-Chrome path is exercised by e2e tests; this
// unit test only locks in the nil-page short-circuit.
func TestActivatePage_NoPageIsNoop(t *testing.T) {
	e := &Executor{}
	if err := e.ActivatePage(); err != nil {
		t.Fatalf("ActivatePage with no page: want nil, got %v", err)
	}
}
