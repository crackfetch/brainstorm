package workflow

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const (
	defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// Executor runs workflow actions against a real browser.
type Executor struct {
	browser    *rod.Browser
	page       *rod.Page
	workflow   *Workflow
	headed     bool
	autoHeaded bool // start headless, escalate to headed on failure
	profileDir string
	debug      bool

	// LastDownload holds the path to the most recently downloaded file.
	LastDownload string
	// LastResult holds the string result of the most recent action (e.g. downloaded CSV content).
	LastResult string
	// LastStatusCode holds the HTTP status code from the most recent navigation.
	LastStatusCode int

	// pendingDownloadWait holds a WaitDownload callback registered before a click
	// that triggers a download. This solves the sequencing issue where rod requires
	// WaitDownload to be called BEFORE the click that triggers the download.
	pendingDownloadWait func() *proto.PageDownloadWillBegin
	pendingDownloadDir  string
}

// NewExecutor creates an executor with browser configuration.
// Use functional options: WithHeaded(true), WithDebug(true), WithProfileDir("...").
func NewExecutor(w *Workflow, opts ...Option) *Executor {
	e := &Executor{workflow: w}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Start launches the browser with stealth settings.
func (e *Executor) Start() error {
	l := launcher.New()

	// Use system Chrome if available, fall back to rod's auto-download
	if path, exists := launcher.LookPath(); exists {
		l = l.Bin(path)
	}

	if e.profileDir != "" {
		os.MkdirAll(e.profileDir, 0755)
		l = l.UserDataDir(e.profileDir)
	}

	l = l.Set("disable-blink-features", "AutomationControlled")

	// Set default viewport via Chrome flags
	vp := DefaultViewport()
	if e.workflow != nil && e.workflow.Viewport != nil {
		vp = *e.workflow.Viewport
	}
	l = l.Set("window-size", fmt.Sprintf("%d,%d", vp.Width, vp.Height))

	if e.headed {
		l = l.Headless(false)
		l = l.Set("window-position", "100,100")
	}

	controlURL, err := l.Launch()
	if err != nil && e.profileDir != "" && strings.Contains(err.Error(), "SingletonLock") {
		if e.removeStaleSingletonLock() {
			// Stale lock removed — retry once with a fresh launcher.
			l2 := launcher.New()
			if path, exists := launcher.LookPath(); exists {
				l2 = l2.Bin(path)
			}
			l2 = l2.UserDataDir(e.profileDir)
			l2 = l2.Set("disable-blink-features", "AutomationControlled")
			l2 = l2.Set("window-size", fmt.Sprintf("%d,%d", vp.Width, vp.Height))
			if e.headed {
				l2 = l2.Headless(false)
				l2 = l2.Set("window-position", "100,100")
			}
			controlURL, err = l2.Launch()
		}
	}
	if err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("connect to browser: %w", err)
	}

	e.browser = browser
	return nil
}

// removeStaleSingletonLock checks whether the SingletonLock in the profile
// directory is held by a dead process. If so, it removes the lock file and
// returns true. If a live process holds the lock, it returns false — we never
// kill another process's Chrome.
func (e *Executor) removeStaleSingletonLock() bool {
	lockPath := filepath.Join(e.profileDir, "SingletonLock")

	// SingletonLock is a symlink whose target encodes "hostname-pid".
	target, err := os.Readlink(lockPath)
	if err != nil {
		// Not a symlink or doesn't exist — try removing anyway.
		if os.Remove(lockPath) == nil {
			log.Printf("Removed stale SingletonLock (unreadable link)")
			return true
		}
		return false
	}

	// Parse PID from "hostname-pid" format.
	parts := strings.SplitN(target, "-", 2)
	if len(parts) == 2 {
		if pid, err := strconv.Atoi(parts[1]); err == nil {
			// Signal 0 checks if the process exists without killing it.
			proc, err := os.FindProcess(pid)
			if err == nil && proc.Signal(syscall.Signal(0)) == nil {
				// Process is alive — do not remove.
				return false
			}
		}
	}

	if err := os.Remove(lockPath); err != nil {
		return false
	}
	log.Printf("Removed stale SingletonLock (owner process is dead)")
	return true
}

// restart shuts down the current browser and relaunches with a new headed mode.
func (e *Executor) restart(headed bool) error {
	e.Close()
	e.headed = headed
	return e.Start()
}

// Close shuts down the browser.
func (e *Executor) Close() {
	if e.browser != nil {
		e.browser.Close()
	}
}

// NavigateTo creates a new page, injects stealth, and navigates to the given URL.
// Used by one-shot commands (inspect, screenshot, eval) that don't need a workflow.
func (e *Executor) NavigateTo(url string) error {
	page, err := e.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return fmt.Errorf("create page: %w", err)
	}
	e.page = page
	e.injectStealth()
	var wfVp *Viewport
	if e.workflow != nil {
		wfVp = e.workflow.Viewport
	}
	e.setViewport(ResolveViewport(wfVp, nil))
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: defaultUserAgent,
	})
	if err := page.Navigate(url); err != nil {
		return fmt.Errorf("navigate to %s: %w", url, err)
	}
	page.MustWaitLoad()
	e.captureStatusCode()
	return nil
}

// RunAction executes a named action from the workflow.
// Returns a structured ActionResult suitable for JSON serialization.
func (e *Executor) RunAction(name string) *ActionResult {
	start := time.Now()
	action, ok := e.workflow.Actions[name]
	if !ok {
		return &ActionResult{
			OK:     false,
			Action: name,
			Error:  fmt.Sprintf("action %q not found in workflow %q", name, e.workflow.Name),
		}
	}

	// Create a new page for each action
	page, err := e.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return &ActionResult{
			OK:         false,
			Action:     name,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      fmt.Sprintf("create page: %v", err),
		}
	}
	e.page = page

	// Inject stealth on every new page
	e.injectStealth()

	// Set viewport (action > workflow > default)
	e.setViewport(ResolveViewport(e.workflow.Viewport, action.Viewport))

	// Set user agent
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: defaultUserAgent,
	})

	// Navigate to action URL if specified
	if action.URL != "" {
		url := InterpolateEnv(action.URL, e.workflow.Env)
		if err := page.Navigate(url); err != nil {
			return &ActionResult{
				OK:         false,
				Action:     name,
				DurationMs: time.Since(start).Milliseconds(),
				Error:      fmt.Sprintf("navigate to %s: %v", url, err),
			}
		}
		page.MustWaitLoad()
		e.captureStatusCode()
	}

	// Execute steps. runSteps returns nil on success, *ActionResult on failure.
	escalated := false
	if failResult := e.runSteps(name, action); failResult != nil {
		// Auto-escalation: if we failed headless on an action that wants headed
		// mode, relaunch the browser headed and retry all steps from scratch.
		if e.autoHeaded && action.Headed && !e.headed {
			log.Printf("[%s] headless attempt failed — escalating to headed mode", name)
			if err := e.restart(true); err != nil {
				failResult.Error += fmt.Sprintf("; headed escalation failed: %v", err)
				failResult.DurationMs = time.Since(start).Milliseconds()
				return failResult
			}
			// Set up a fresh page for the headed retry.
			retryPage, err := e.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
			if err != nil {
				failResult.Error += fmt.Sprintf("; headed page create failed: %v", err)
				failResult.DurationMs = time.Since(start).Milliseconds()
				return failResult
			}
			e.page = retryPage
			e.injectStealth()
			e.setViewport(ResolveViewport(e.workflow.Viewport, action.Viewport))
			retryPage.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
				UserAgent: defaultUserAgent,
			})
			if action.URL != "" {
				url := InterpolateEnv(action.URL, e.workflow.Env)
				if err := retryPage.Navigate(url); err != nil {
					failResult.Error += fmt.Sprintf("; headed navigate failed: %v", err)
					failResult.DurationMs = time.Since(start).Milliseconds()
					return failResult
				}
				retryPage.MustWaitLoad()
			}
			// Retry all steps in headed mode.
			if retryResult := e.runSteps(name, action); retryResult != nil {
				retryResult.Escalated = true
				retryResult.DurationMs = time.Since(start).Milliseconds()
				return retryResult
			}
			escalated = true
		} else {
			failResult.DurationMs = time.Since(start).Milliseconds()
			return failResult
		}
	}

	// Build success result.
	result := &ActionResult{
		OK:         true,
		Action:     name,
		Steps:      len(action.Steps),
		DurationMs: time.Since(start).Milliseconds(),
		Escalated:  escalated,
		StatusCode: e.LastStatusCode,
	}

	// Attach download info if a file was downloaded during this action.
	if e.LastDownload != "" {
		result.Download = e.LastDownload
		if info, err := os.Stat(e.LastDownload); err == nil {
			result.DownloadSize = info.Size()
		}
	}

	// Run post-action eval assertions.
	if evalResult := e.runEvals(name, action); evalResult != nil {
		result.EvalsPassed = evalResult.Passed
		result.EvalsFailed = evalResult.Failed
		result.EvalErrors = evalResult.Errors
		if evalResult.Failed > 0 {
			result.OK = false
			result.Error = fmt.Sprintf("%d of %d eval assertions failed", evalResult.Failed, evalResult.Passed+evalResult.Failed)
			result.PageURL, result.PageHTML = e.capturePageState()
		}
	}

	return result
}

// runSteps executes the steps of an action. Returns nil on success,
// or an *ActionResult describing the failure.
func (e *Executor) runSteps(name string, action Action) *ActionResult {
	for i, step := range action.Steps {
		label := step.Label
		if label == "" {
			label = fmt.Sprintf("step %d", i+1)
		}

		if e.debug {
			log.Printf("[%s] executing: %s", name, label)
		}

		// Look ahead: if the NEXT step is a download, register WaitDownload now
		// (before executing the current click step).
		if i+1 < len(action.Steps) && action.Steps[i+1].Download != nil {
			if step.Click != nil {
				downloadDir := filepath.Join(os.TempDir(), "brz-downloads")
				os.MkdirAll(downloadDir, 0755)
				e.pendingDownloadWait = e.browser.WaitDownload(downloadDir)
				e.pendingDownloadDir = downloadDir
				if e.debug {
					log.Printf("[%s] pre-registered WaitDownload before click", name)
				}
			}
		}

		if err := e.executeStep(step); err != nil {
			if step.Optional {
				log.Printf("[%s] optional step %d failed (non-fatal): %v", name, i+1, err)
				continue
			}

			screenshotPath := fmt.Sprintf("%s_failed_%s_%d.png", name, time.Now().Format("20060102-150405"), i)
			e.takeScreenshot(screenshotPath)

			result := &ActionResult{
				OK:         false,
				Action:     name,
				Steps:      i,
				Error:      fmt.Sprintf("action %q, %s: %v", name, label, err),
				FailedStep: i + 1,
				StepType:   stepType(step),
				Screenshot: filepath.Join(os.TempDir(), screenshotPath),
			}

			// Capture page state for debugging.
			result.PageURL, result.PageHTML = e.capturePageState()

			return result
		}
	}
	return nil
}

// stepType returns the type name of a step for error reporting.
func stepType(s Step) string {
	switch {
	case s.Navigate != "":
		return "navigate"
	case s.Click != nil:
		return "click"
	case s.Fill != nil:
		return "fill"
	case s.Select != nil:
		return "select"
	case s.Upload != nil:
		return "upload"
	case s.Download != nil:
		return "download"
	case s.WaitVisible != nil:
		return "wait_visible"
	case s.WaitText != nil:
		return "wait_text"
	case s.WaitURL != nil:
		return "wait_url"
	case s.Screenshot != "":
		return "screenshot"
	case s.Sleep != nil:
		return "sleep"
	case s.Eval != "":
		return "eval"
	default:
		return "unknown"
	}
}

func (e *Executor) executeStep(step Step) error {
	switch {
	case step.Navigate != "":
		url := InterpolateEnv(step.Navigate, e.workflow.Env)
		if err := e.page.Navigate(url); err != nil {
			return fmt.Errorf("navigate: %w", err)
		}
		e.page.MustWaitLoad()
		e.captureStatusCode()

	case step.Click != nil:
		return e.doClick(step.Click)

	case step.Fill != nil:
		return e.doFill(step.Fill)

	case step.Select != nil:
		return e.doSelect(step.Select)

	case step.Upload != nil:
		return e.doUpload(step.Upload)

	case step.Download != nil:
		return e.doDownload(step.Download)

	case step.WaitVisible != nil:
		return e.doWaitVisible(step.WaitVisible)

	case step.WaitText != nil:
		return e.doWaitText(step.WaitText)

	case step.WaitURL != nil:
		return e.doWaitURL(step.WaitURL)

	case step.Screenshot != "":
		e.takeScreenshot(step.Screenshot)

	case step.Sleep != nil:
		d := ParseTimeout(step.Sleep.Duration)
		time.Sleep(d)

	case step.Eval != "":
		js := InterpolateEnv(step.Eval, e.workflow.Env)
		_, err := e.page.Eval(js)
		if err != nil {
			return fmt.Errorf("eval: %w", err)
		}

	default:
		return fmt.Errorf("empty step (no action specified)")
	}

	return nil
}

func (e *Executor) doClick(c *ClickStep) error {
	timeout := ParseTimeout(c.Timeout)
	selector := c.Selector

	var el *rod.Element
	var err error

	if c.Text != "" {
		// Find by selector + text content, polling until timeout.
		// Elements() returns a snapshot (doesn't wait), so we poll.
		deadline := time.Now().Add(timeout)
		pollInterval := 500 * time.Millisecond
		for {
			els, err := e.page.Elements(selector)
			if err == nil {
				for _, candidate := range els {
					text, _ := candidate.Text()
					if strings.Contains(text, c.Text) {
						el = candidate
						break
					}
				}
			}
			if el != nil {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("no element matching %q with text %q", selector, c.Text)
			}
			time.Sleep(pollInterval)
		}
	} else if c.Nth > 0 {
		els, err := e.page.Timeout(timeout).Elements(selector)
		if err != nil {
			return fmt.Errorf("find elements %q: %w", selector, err)
		}
		if c.Nth >= len(els) {
			return fmt.Errorf("selector %q: nth=%d but only %d elements found", selector, c.Nth, len(els))
		}
		el = els[c.Nth]
	} else {
		el, err = e.page.Timeout(timeout).Element(selector)
		if err != nil {
			return fmt.Errorf("find element %q: %w", selector, err)
		}
	}

	return el.Click(proto.InputMouseButtonLeft, 1)
}

func (e *Executor) doFill(f *FillStep) error {
	el, err := e.page.Timeout(30 * time.Second).Element(f.Selector)
	if err != nil {
		return fmt.Errorf("find element %q: %w", f.Selector, err)
	}

	if f.Clear {
		el.MustSelectAllText().MustInput("")
	}

	value := InterpolateEnv(f.Value, e.workflow.Env)
	return el.Input(value)
}

func (e *Executor) doSelect(s *SelectStep) error {
	timeout := 5 * time.Second
	if s.Timeout != "" {
		timeout = ParseTimeout(s.Timeout)
	}

	el, err := e.page.Timeout(timeout).Element(s.Selector)
	if err != nil {
		return fmt.Errorf("find element %q: %w", s.Selector, err)
	}

	value := InterpolateEnv(s.Value, e.workflow.Env)
	text := InterpolateEnv(s.Text, e.workflow.Env)

	if value == "" && text == "" {
		return fmt.Errorf("select step requires either value or text")
	}

	// Build JS that detects the dropdown type and selects the value.
	// Returns a string: "ok", "disabled", "not_found", or an error message.
	js := `function(value, text) {
		var el = this;

		// Check if disabled
		if (el.disabled) return 'disabled';

		// Detect dropdown type
		var isSelect2 = el.classList.contains('select2-hidden-accessible') ||
			(el.nextElementSibling && el.nextElementSibling.classList.contains('select2-container'));

		// Determine which value to set
		var targetValue = value;
		if (!value && text) {
			// Find option by visible text
			var opts = el.querySelectorAll('option');
			var found = false;
			for (var i = 0; i < opts.length; i++) {
				if (opts[i].textContent.trim() === text) {
					targetValue = opts[i].value;
					found = true;
					break;
				}
			}
			if (!found) return 'not_found_text:' + text;
		}

		// Verify the value exists in options
		if (targetValue) {
			var optValues = Array.from(el.querySelectorAll('option')).map(function(o) { return o.value; });
			if (optValues.indexOf(targetValue) === -1) return 'not_found_value:' + targetValue;
		}

		// Set the value
		el.value = targetValue;

		if (isSelect2 && typeof jQuery !== 'undefined') {
			// Select2: use jQuery API to trigger change
			jQuery(el).val(targetValue).trigger('change');
		} else {
			// Native: dispatch change and input events
			el.dispatchEvent(new Event('change', {bubbles: true}));
			el.dispatchEvent(new Event('input', {bubbles: true}));
		}

		return 'ok';
	}`

	res, err := el.Eval(js, value, text)
	if err != nil {
		return fmt.Errorf("select eval: %w", err)
	}

	result := res.Value.Str()
	switch {
	case result == "ok":
		return nil
	case result == "disabled":
		return fmt.Errorf("select %q is disabled", s.Selector)
	case len(result) > 16 && result[:16] == "not_found_text:":
		return fmt.Errorf("no option with text %q in %q", result[16:], s.Selector)
	case len(result) > 17 && result[:17] == "not_found_value:":
		return fmt.Errorf("no option with value %q in %q", result[17:], s.Selector)
	default:
		return fmt.Errorf("select failed: %s", result)
	}
}

func (e *Executor) doUpload(u *UploadStep) error {
	el, err := e.page.Timeout(30 * time.Second).Element(u.Selector)
	if err != nil {
		return fmt.Errorf("find file input %q: %w", u.Selector, err)
	}

	var filePath string
	if u.Source == "result" {
		// Use the last downloaded/generated file
		filePath = e.LastDownload
	} else {
		filePath = InterpolateEnv(u.Source, e.workflow.Env)
	}

	if filePath == "" {
		return fmt.Errorf("no file to upload (source=%q)", u.Source)
	}

	el.MustSetFiles(filePath)
	return nil
}

func (e *Executor) doDownload(d *DownloadStep) error {
	var wait func() *proto.PageDownloadWillBegin
	var downloadDir string

	if e.pendingDownloadWait != nil {
		// Use the pre-registered wait (set up before the triggering click).
		wait = e.pendingDownloadWait
		downloadDir = e.pendingDownloadDir
		e.pendingDownloadWait = nil
		e.pendingDownloadDir = ""
	} else {
		// Fallback: register now (only works if the download was already triggered).
		downloadDir = filepath.Join(os.TempDir(), "brz-downloads")
		os.MkdirAll(downloadDir, 0755)
		wait = e.browser.WaitDownload(downloadDir)
	}

	// Block until download finishes
	info := wait()

	if info == nil {
		return fmt.Errorf("download failed: no download info received")
	}

	// The downloaded file is saved in downloadDir with the GUID as filename
	downloadPath := filepath.Join(downloadDir, info.GUID)
	e.LastDownload = downloadPath

	// Read file content into LastResult
	if data, err := os.ReadFile(downloadPath); err == nil {
		e.LastResult = string(data)
	}

	return nil
}

func (e *Executor) doWaitVisible(w *WaitStep) error {
	timeout := ParseTimeout(w.Timeout)
	_, err := e.page.Timeout(timeout).Element(w.Selector)
	return err
}

func (e *Executor) doWaitText(w *WaitStep) error {
	timeout := ParseTimeout(w.Timeout)
	_, err := e.page.Timeout(timeout).ElementR("*", w.Text)
	return err
}

func (e *Executor) doWaitURL(w *WaitURLStep) error {
	timeout := ParseTimeout(w.Timeout)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		info, err := e.page.Info()
		if err == nil && strings.Contains(info.URL, w.Match) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("URL did not match %q within %s", w.Match, timeout)
}

// setViewport configures the page viewport via CDP. The viewport parameter
// is resolved from action > workflow > default before calling this.
func (e *Executor) setViewport(vp Viewport) {
	_ = proto.EmulationSetDeviceMetricsOverride{
		Width:             vp.Width,
		Height:            vp.Height,
		DeviceScaleFactor: 1,
	}.Call(e.page)
}

func (e *Executor) injectStealth() {
	e.page.MustEval(`() => {
		Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
	}`)
}

func (e *Executor) takeScreenshot(name string) {
	data, err := e.page.Screenshot(true, nil)
	if err != nil {
		if e.debug {
			log.Printf("screenshot failed: %v", err)
		}
		return
	}

	path := filepath.Join(os.TempDir(), name)
	os.WriteFile(path, data, 0644)
	if e.debug {
		log.Printf("screenshot saved: %s", path)
	}
}

// capturePageState returns the current page URL and HTML for debugging.
// captureStatusCode reads the HTTP status code of the most recent navigation
// using the Navigation Timing API. Works in Chrome 109+. Falls back to 0
// if the API is unavailable or the page hasn't navigated.
func (e *Executor) captureStatusCode() {
	if e.page == nil {
		return
	}
	res, err := e.page.Eval(`() => {
		const nav = performance.getEntriesByType('navigation');
		return nav.length > 0 ? nav[0].responseStatus : 0;
	}`)
	if err != nil {
		return
	}
	if code := res.Value.Int(); code > 0 {
		e.LastStatusCode = code
	}
}

func (e *Executor) capturePageState() (pageURL, pageHTML string) {
	if e.page == nil {
		return "", ""
	}
	if info, err := e.page.Info(); err == nil {
		pageURL = info.URL
	}
	if html, err := e.page.HTML(); err == nil {
		pageHTML = html
	}
	return
}

// WaitOnFailure keeps the browser open for inspection when headed and an
// action failed. Call this from the CLI layer after RunAction returns a failure.
// Waits for the user to press Enter in the terminal, then returns.
func (e *Executor) WaitOnFailure() {
	if !e.headed || e.page == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "\nBrowser kept open for debugging. Press Enter to close...\n")
	buf := make([]byte, 1)
	os.Stdin.Read(buf)
}

// Page returns the current page for advanced usage.
func (e *Executor) Page() *rod.Page {
	return e.page
}

// KeyPress sends a keyboard input to the current page.
func (e *Executor) KeyPress(key input.Key) error {
	return e.page.Keyboard.Press(key)
}

// IsHeaded returns whether the browser is in headed (visible) mode.
func (e *Executor) IsHeaded() bool {
	return e.headed
}

// SetEnv sets an environment variable in the workflow's env map.
// These are used by InterpolateEnv for ${VAR} substitution in step values.
func (e *Executor) SetEnv(key, value string) {
	if e.workflow.Env == nil {
		e.workflow.Env = make(map[string]string)
	}
	e.workflow.Env[key] = value
}
