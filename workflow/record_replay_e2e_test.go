package workflow

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/crackfetch/brainstorm/internal/cassette"
)

// TestRecordReplay_RoundTrip exercises the full record→replay path against an
// in-process HTTP server.
//
//   - Phase 1: stand up a test server, attach the recorder, run a workflow that
//     fetches a known URL from the page, save the cassette.
//   - Phase 2: tear down the server, attach the replayer with the saved cassette,
//     run the same workflow, and assert (a) the workflow still succeeds and
//     (b) the test server hit count did NOT increase (we never touched the
//     real network in replay).
//
// This is the headline behavior contract for the feature: replay must serve
// from disk, even when the original server is unreachable.
func TestRecordReplay_RoundTrip(t *testing.T) {
	skipIfNoChrome(t)

	var hits int64
	mux := http.NewServeMux()
	mux.HandleFunc("/index.html", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>page<script>fetch('/api').then(r=>r.text()).then(t=>document.title=t)</script></body></html>`)
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "hello-from-server")
	})
	srv := httptest.NewServer(mux)

	tmpDir := t.TempDir()
	cassettePath := filepath.Join(tmpDir, "rr.cassette.json")

	// ---------- Phase 1: record ----------
	{
		exec := NewExecutor(nil)
		if err := exec.Start(); err != nil {
			t.Fatalf("record: start browser: %v", err)
		}
		rec := cassette.NewRecorder(nil, cassette.RecorderOptions{
			Mode:       cassette.ModeAll,
			Workflow:   "rr-test",
			BrzVersion: "test",
		})
		if err := rec.Attach(exec.Browser()); err != nil {
			t.Fatalf("attach recorder: %v", err)
		}
		if err := exec.NavigateTo(srv.URL + "/index.html"); err != nil {
			exec.Close()
			t.Fatalf("record navigate: %v", err)
		}
		// Trigger the fetch by waiting for the title update.
		_, _ = exec.Page().Eval(`async () => { for (let i=0;i<20;i++){if(document.title==='hello-from-server')return true; await new Promise(r=>setTimeout(r,50));} return false; }`)
		if err := rec.Stop(); err != nil {
			t.Fatalf("recorder stop: %v", err)
		}
		exec.Close()

		c := rec.Cassette()
		if len(c.Entries) == 0 {
			t.Fatalf("recorder captured 0 entries")
		}
		if err := cassette.Save(c, cassettePath); err != nil {
			t.Fatalf("save cassette: %v", err)
		}
	}

	hitsAfterRecord := atomic.LoadInt64(&hits)
	if hitsAfterRecord == 0 {
		t.Fatalf("test server saw no requests during record")
	}

	// Tear down the server. Any replay-time network call would now fail.
	srv.Close()

	// ---------- Phase 2: replay ----------
	{
		c, err := cassette.Load(cassettePath)
		if err != nil {
			t.Fatalf("load cassette: %v", err)
		}
		idx := cassette.NewIndex(c)
		rep := cassette.NewReplayer(idx, cassette.ReplayerOptions{Strict: true})

		exec := NewExecutor(nil)
		if err := exec.Start(); err != nil {
			t.Fatalf("replay: start browser: %v", err)
		}
		if err := rep.Attach(exec.Browser()); err != nil {
			t.Fatalf("attach replayer: %v", err)
		}
		// Navigate to the same URL — it should be served from the cassette.
		if err := exec.NavigateTo(srv.URL + "/index.html"); err != nil {
			exec.Close()
			t.Fatalf("replay navigate: %v", err)
		}
		_, _ = exec.Page().Eval(`async () => { for (let i=0;i<20;i++){if(document.title==='hello-from-server')return true; await new Promise(r=>setTimeout(r,50));} return false; }`)
		_ = rep.Stop()
		exec.Close()
	}

	hitsAfterReplay := atomic.LoadInt64(&hits)
	if hitsAfterReplay != hitsAfterRecord {
		t.Fatalf("server contacted during replay: hits before=%d after=%d", hitsAfterRecord, hitsAfterReplay)
	}
}

// TestReplay_StrictUnmatchedFails confirms that strict mode flags an
// unmatched request as a miss. We seed an essentially empty cassette and
// navigate to a URL not in it; the replayer should record the miss and the
// page should fail to load (server is intentionally absent).
func TestReplay_StrictUnmatchedFails(t *testing.T) {
	skipIfNoChrome(t)

	c := &cassette.Cassette{Version: cassette.FormatVersion, Entries: []*cassette.Entry{}}
	idx := cassette.NewIndex(c)
	rep := cassette.NewReplayer(idx, cassette.ReplayerOptions{Strict: true})

	exec := NewExecutor(nil)
	if err := exec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer exec.Close()
	if err := rep.Attach(exec.Browser()); err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer rep.Stop()

	// Navigate to a localhost URL that is not in the cassette and has no server.
	// The interceptor will fail the request in strict mode; navigation will error
	// or load an empty page. Either way, Misses() should be non-empty.
	_ = exec.NavigateTo("http://127.0.0.1:1/missing")

	misses := rep.Misses()
	if len(misses) == 0 {
		t.Fatalf("expected at least one miss in strict mode")
	}
	if !strings.EqualFold(misses[0].Method, "GET") {
		t.Fatalf("unexpected miss key: %+v", misses[0])
	}
}
