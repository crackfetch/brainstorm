package cassette

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// RecordMode controls record-mode behavior when a request is already in the
// cassette (only relevant for ModeNew).
type RecordMode string

const (
	// ModeAll records every intercepted request, even if the same key is
	// already in the cassette. The cassette grows on each run.
	ModeAll RecordMode = "all"
	// ModeNew passes through to the network for already-recorded requests
	// but does not append a new entry. New requests get appended.
	ModeNew RecordMode = "new"
)

// IsValidMode reports whether m is a recognized record mode.
func IsValidMode(m RecordMode) bool {
	switch m {
	case ModeAll, ModeNew:
		return true
	}
	return false
}

// RecorderOptions configures the Recorder.
type RecorderOptions struct {
	Mode RecordMode
	// BodyCapBytes caps each captured response body. 0 = use DefaultBodyCapBytes;
	// negative = no cap. The cap is enforced while reading the response (LimitReader),
	// not after, so a 500MB response will not be fully buffered into memory.
	BodyCapBytes int
	Stderr       io.Writer
	Workflow     string
	BrzVersion   string
}

// Recorder hooks rod.Browser.HijackRequests, fetches the upstream response
// via Go's http.Client, and appends entries to a Cassette. Call Stop()
// before saving the cassette — Stop drains in-flight handlers via a WaitGroup
// so concurrent intercepts cannot race the save.
type Recorder struct {
	mu       sync.Mutex
	cassette *Cassette
	router   *rod.HijackRouter
	client   *http.Client
	opts     RecorderOptions
	known    map[Key]bool // for ModeNew dedup; protected by mu
	wg       sync.WaitGroup
}

// NewRecorder constructs a Recorder. existing may be nil; if non-nil, its
// entries seed the recorder so ModeNew can deduplicate against prior runs.
func NewRecorder(existing *Cassette, opts RecorderOptions) *Recorder {
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Mode == "" {
		opts.Mode = ModeAll
	}
	if opts.BodyCapBytes == 0 {
		opts.BodyCapBytes = DefaultBodyCapBytes
	}
	c := existing
	if c == nil {
		c = &Cassette{
			Version:    FormatVersion,
			RecordedAt: time.Now().UTC(),
			BrzVersion: opts.BrzVersion,
			Workflow:   opts.Workflow,
			Entries:    []*Entry{},
		}
	}
	r := &Recorder{cassette: c, opts: opts, known: make(map[Key]bool)}
	for _, e := range c.Entries {
		body, _ := DecodeBody(e.Request.BodyB64)
		r.known[MakeKey(e.Request.Method, e.Request.URL, body)] = true
	}
	// Don't follow redirects automatically — we want to record each hop.
	r.client = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 60 * time.Second,
	}
	return r
}

// Attach starts the interceptor on the given browser. Must be called after
// the browser is connected via CDP. Returns an error if Fetch.enable fails.
func (r *Recorder) Attach(browser *rod.Browser) error {
	router := browser.HijackRequests()
	r.router = router
	if err := router.Add("*", "", r.handle); err != nil {
		return fmt.Errorf("recorder: enable Fetch: %w", err)
	}
	go router.Run()
	return nil
}

// Stop tears down the interceptor and waits for any in-flight handlers to
// finish writing to the cassette. It is safe to call Cassette() after Stop().
func (r *Recorder) Stop() error {
	if r.router == nil {
		return nil
	}
	err := r.router.Stop()
	r.wg.Wait()
	return err
}

// Cassette returns the current cassette state.
func (r *Recorder) Cassette() *Cassette { return r.cassette }

func (r *Recorder) handle(h *rod.Hijack) {
	r.wg.Add(1)
	defer r.wg.Done()

	rawURL := h.Request.URL().String()
	method := h.Request.Method()
	body := h.Request.Body()

	// Skip data: URLs — let them resolve internally; nothing to record.
	if strings.HasPrefix(rawURL, "data:") {
		h.ContinueRequest(&proto.FetchContinueRequest{})
		return
	}

	bodyBytes := []byte(body)
	key := MakeKey(method, rawURL, bodyBytes)

	// ModeNew dedup decision under the lock so two concurrent identical
	// requests don't both append. We do not "reserve" the key before the
	// network call: the canonical contract is "if the key was in the loaded
	// cassette, don't re-record." Two new in-flight identical requests in
	// ModeNew will both record (rare, and harmless: the second is the same
	// content). Truly idempotent dedup would require pre-reservation and
	// blocking duplicates on the same in-flight result, which is more
	// complexity than this feature warrants for v1.
	r.mu.Lock()
	alreadyKnown := r.known[key]
	r.mu.Unlock()

	// Stream the upstream response via Go's http.Client so we can enforce
	// the body cap with a LimitReader rather than buffering the whole body
	// (rod's HijackResponse.LoadResponse does the latter, which makes the
	// cap claim a lie for very large responses).
	respCode, respHeaders, respBytes, truncated, err := r.fetchUpstream(method, rawURL, bodyBytes, h.Request.Headers())
	if err != nil {
		fmt.Fprintf(r.opts.Stderr, "recorder: passthrough failed for %s %s: %v\n", method, rawURL, err)
		h.Response.Fail(proto.NetworkErrorReasonFailed)
		return
	}
	if truncated {
		fmt.Fprintf(r.opts.Stderr, "recorder: response body for %s %s exceeded %d bytes, truncating (use --no-body-cap to disable)\n",
			method, rawURL, r.opts.BodyCapBytes)
	}

	// Fulfill the browser request with what we got. We deliberately drop
	// Content-Length and Content-Encoding here — Go's transport already
	// decoded any gzip/deflate, so the bytes we pass to the browser are
	// the decoded payload and the original encoding header would mislead
	// the renderer. The browser will compute Content-Length from Body.
	payload := h.Response.Payload()
	payload.ResponseCode = respCode
	payload.Body = respBytes
	for k, v := range respHeaders {
		if isHopByHopOrEncodingHeader(k) {
			continue
		}
		h.Response.SetHeader(k, v)
	}

	if r.opts.Mode == ModeNew && alreadyKnown {
		return
	}

	reqHeaders := flattenHeaders(h.Request.Headers())
	entry := &Entry{
		Request: Request{
			Method:  method,
			URL:     rawURL,
			Headers: reqHeaders,
			BodyB64: EncodeBody(bodyBytes),
		},
		Response: Response{
			Status:  respCode,
			Headers: respHeaders,
			BodyB64: EncodeBody(respBytes),
		},
		BodyTruncated: truncated,
	}

	r.mu.Lock()
	r.cassette.Entries = append(r.cassette.Entries, entry)
	r.known[key] = true
	r.mu.Unlock()
}

// fetchUpstream issues the request via Go's http.Client and returns the
// response with its body capped to opts.BodyCapBytes (using io.LimitReader
// so we never buffer more than the cap into memory). truncated is true
// when the response was longer than the cap.
func (r *Recorder) fetchUpstream(method, rawURL string, body []byte, headers proto.NetworkHeaders) (int, map[string]string, []byte, bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, nil, false, err
	}
	req, err := http.NewRequest(method, u.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, false, err
	}
	for k, v := range headers {
		req.Header.Set(k, v.String())
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, nil, nil, false, err
	}
	defer resp.Body.Close()

	cap := r.opts.BodyCapBytes
	var reader io.Reader = resp.Body
	if cap > 0 {
		// Read one byte past cap so we can detect truncation.
		reader = io.LimitReader(resp.Body, int64(cap)+1)
	}
	buf, err := io.ReadAll(reader)
	if err != nil {
		return 0, nil, nil, false, err
	}
	truncated := false
	if cap > 0 && len(buf) > cap {
		buf = buf[:cap]
		truncated = true
		// Drain the rest so the connection can be reused. Cap drained read too.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, int64(cap)))
	}

	headersOut := map[string]string{}
	for k, vs := range resp.Header {
		headersOut[k] = strings.Join(vs, ", ")
	}
	return resp.StatusCode, headersOut, buf, truncated, nil
}

func isHopByHopOrEncodingHeader(k string) bool {
	switch strings.ToLower(k) {
	case "content-length", "content-encoding", "transfer-encoding",
		"connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "upgrade":
		return true
	}
	return false
}

func flattenHeaders(h proto.NetworkHeaders) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		m[k] = v.String()
	}
	return m
}

// ReplayerOptions configures the Replayer.
type ReplayerOptions struct {
	Strict bool // fail unmatched requests instead of passing through
	Stderr io.Writer
}

// Replayer serves browser requests from a cassette index. Unmatched requests
// either pass through (default) or fail (strict). Strict mode is suitable
// for CI: a regression in workflow ordering surfaces as a clear failure.
type Replayer struct {
	idx    *Index
	router *rod.HijackRouter
	opts   ReplayerOptions
	client *http.Client
	missMu sync.Mutex
	misses []Key
	wg     sync.WaitGroup
}

// NewReplayer constructs a Replayer over the given cassette index.
func NewReplayer(idx *Index, opts ReplayerOptions) *Replayer {
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	return &Replayer{
		idx:    idx,
		opts:   opts,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Misses returns the keys that did not match the cassette during the run.
func (r *Replayer) Misses() []Key {
	r.missMu.Lock()
	defer r.missMu.Unlock()
	out := make([]Key, len(r.misses))
	copy(out, r.misses)
	return out
}

// Attach starts the interceptor on the given browser.
func (r *Replayer) Attach(browser *rod.Browser) error {
	router := browser.HijackRequests()
	r.router = router
	if err := router.Add("*", "", r.handle); err != nil {
		return fmt.Errorf("replayer: enable Fetch: %w", err)
	}
	go router.Run()
	return nil
}

// Stop tears down the interceptor and waits for handlers to drain.
func (r *Replayer) Stop() error {
	if r.router == nil {
		return nil
	}
	err := r.router.Stop()
	r.wg.Wait()
	return err
}

func (r *Replayer) handle(h *rod.Hijack) {
	r.wg.Add(1)
	defer r.wg.Done()

	rawURL := h.Request.URL().String()
	method := h.Request.Method()
	bodyBytes := []byte(h.Request.Body())

	if strings.HasPrefix(rawURL, "data:") {
		h.ContinueRequest(&proto.FetchContinueRequest{})
		return
	}

	key := MakeKey(method, rawURL, bodyBytes)
	entry, ok := r.idx.Lookup(key)
	if !ok {
		r.missMu.Lock()
		r.misses = append(r.misses, key)
		r.missMu.Unlock()
		if r.opts.Strict {
			fmt.Fprintf(r.opts.Stderr, "replay (strict): unmatched %s\n", key)
			h.Response.Fail(proto.NetworkErrorReasonFailed)
			return
		}
		fmt.Fprintf(r.opts.Stderr, "replay: unmatched, passing through to network: %s\n", key)
		// Pass through to the real network in non-strict mode. This lets a
		// partial cassette degrade gracefully (e.g. cassette covers the API
		// but not images).
		if err := h.LoadResponse(r.client, true); err != nil {
			h.Response.Fail(proto.NetworkErrorReasonFailed)
		}
		return
	}

	// A truncated entry means we never captured the full response. Replaying
	// it as-is can confuse the page (mismatched Content-Length, partial JSON,
	// etc.), so we fail strictly and emit a warning. Authors who want to
	// replay known-truncated entries should re-record with --no-body-cap.
	if entry.BodyTruncated {
		fmt.Fprintf(r.opts.Stderr, "replay: refusing to serve truncated entry for %s (re-record with --no-body-cap)\n", key)
		h.Response.Fail(proto.NetworkErrorReasonFailed)
		return
	}

	respBytes, _ := DecodeBody(entry.Response.BodyB64)
	payload := h.Response.Payload()
	payload.ResponseCode = entry.Response.Status
	payload.Body = respBytes
	for k, v := range entry.Response.Headers {
		if isHopByHopOrEncodingHeader(k) {
			continue
		}
		h.Response.SetHeader(k, v)
	}
}

// LoadCassetteForRecord loads an existing cassette for ModeNew seeding.
// Returns (nil, nil) if the file does not exist (clean start). All other
// errors — corrupt JSON, future format version — are surfaced so the caller
// does not silently overwrite a recoverable cassette on save.
func LoadCassetteForRecord(path string) (*Cassette, error) {
	c, err := Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}
