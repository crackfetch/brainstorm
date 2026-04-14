package workflow

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const (
	// fallbackUserAgent is only used if we fail to retrieve the real UA from the browser.
	fallbackUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// headlessChromeUATokenPattern matches the canonical "HeadlessChrome/<version>"
// form Chrome injects into its User-Agent when running headless. We deliberately
// require the trailing "/<digit>" so we never rewrite stray substrings (e.g.
// custom-build annotations or unknown future tokens), which would be silent
// data corruption.
var headlessChromeUATokenPattern = regexp.MustCompile(`HeadlessChrome/(\d)`)

// Executor runs workflow actions against a real browser.
//
// Threading contract: Executor is safe for concurrent use by multiple
// goroutines. All public methods acquire mu before accessing internal state.
// Private methods assume the caller already holds the lock and must NOT
// lock mu themselves, preventing recursive lock deadlocks.
type Executor struct {
	mu sync.Mutex

	browser    *rod.Browser
	page       *rod.Page
	workflow   *Workflow
	headed     bool
	autoHeaded bool // start headless, escalate to headed on failure
	profileDir string
	debug      bool

	// userAgent is the real User-Agent from the running Chrome instance.
	// Set in Start() after connecting to the browser.
	userAgent string

	// userAgentMeta holds Client Hints metadata (brands, platform, etc.)
	// to override navigator.userAgentData and Sec-CH-UA headers. Built in
	// Start() from the browser version, stripping any HeadlessChrome brands.
	userAgentMeta *proto.EmulationUserAgentMetadata

	// LastDownload holds the path to the most recently downloaded file.
	LastDownload string
	// LastResult holds the string result of the most recent action (e.g. downloaded CSV content).
	LastResult string
	// LastStatusCode holds the HTTP status code from the most recent navigation.
	LastStatusCode int

	// cachedProduct stores the browser product string (e.g. "HeadlessChrome/131.0.6778.86")
	// from the last BrowserGetVersion call. Used to detect when the browser has
	// reconnected with a different version, triggering a UA refresh.
	cachedProduct string

	// chromeVersion caches the detected Chrome major version (e.g. 131).
	// 0 means unknown (detection failed or not yet run). Used to gate
	// --headless=new which requires Chrome 109+.
	chromeVersion int

	// pendingDownloadWait holds a WaitDownload callback registered before a click
	// that triggers a download. This solves the sequencing issue where rod requires
	// WaitDownload to be called BEFORE the click that triggers the download.
	pendingDownloadWait func() *proto.PageDownloadWillBegin
	pendingDownloadDir  string
}

// NewExecutor creates an executor with browser configuration.
// Use functional options: WithHeaded(true), WithDebug(true), WithProfileDir("...").
func NewExecutor(w *Workflow, opts ...Option) *Executor {
	e := &Executor{workflow: w, userAgent: fallbackUserAgent}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// UserAgent returns the User-Agent string the executor uses for page requests.
func (e *Executor) UserAgent() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.userAgent
}

// resolveUserAgent returns browserUA if non-empty, otherwise fallbackUserAgent.
// In headless mode Chrome reports its UA as "HeadlessChrome/X" via
// Browser.getVersion. That token is one of the most visible headless
// detection vectors in navigator.userAgent, so we rewrite the canonical
// "HeadlessChrome/<version>" form to plain "Chrome/<version>". Client Hints
// (navigator.userAgentData / Sec-CH-UA headers) are handled separately by
// buildUserAgentMetadata which populates UserAgentMetadata on every page.
func resolveUserAgent(browserUA string) string {
	if browserUA == "" {
		return fallbackUserAgent
	}
	return headlessChromeUATokenPattern.ReplaceAllString(browserUA, "Chrome/$1")
}

// refreshUserAgent updates the cached User-Agent and Client Hints metadata
// if the browser product string has changed (e.g. after a DevTools reconnect).
// When the product matches cachedProduct, this is a no-op.
func (e *Executor) refreshUserAgent(browserUA, browserProduct string) {
	if browserProduct == e.cachedProduct {
		return
	}
	e.cachedProduct = browserProduct
	e.userAgent = resolveUserAgent(browserUA)
	e.userAgentMeta = buildUserAgentMetadata(browserProduct)
}

// refreshUserAgentFromBrowser queries BrowserGetVersion and updates the cached
// UA fields if the product has changed. Called before MustSetUserAgent in
// NavigateTo and setupPage to handle silent browser reconnects.
func (e *Executor) refreshUserAgentFromBrowser() {
	if e.browser == nil {
		return
	}
	ver, err := (proto.BrowserGetVersion{}).Call(e.browser)
	if err != nil {
		return
	}
	e.refreshUserAgent(ver.UserAgent, ver.Product)
}

// chromeVersionPattern extracts the major version from a Chrome product string
// like "HeadlessChrome/131.0.6778.86" or "Chrome/131.0.6778.86".
var chromeVersionPattern = regexp.MustCompile(`(?:Headless)?Chrome/(\d+)\.(\d+\.\d+\.\d+)`)

// chromeCLIVersionPattern matches the output of `chrome --version`, e.g.
// "Google Chrome 131.0.6778.86" or "Chromium 90.0.4430.212 built on Debian".
var chromeCLIVersionPattern = regexp.MustCompile(`(?:Google Chrome|Chromium)\s+(\d+)`)

// parseChromeVersion extracts the major version number from Chrome's --version
// output (e.g. "Google Chrome 131.0.6778.86" -> 131). Returns 0 on parse failure.
func parseChromeVersion(output string) int {
	m := chromeCLIVersionPattern.FindStringSubmatch(output)
	if m == nil {
		return 0
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return v
}

// detectChromeVersion runs `<binPath> --version` and returns the major version.
// Returns 0 if the binary can't be run or the output can't be parsed.
func detectChromeVersion(binPath string) int {
	out, err := exec.Command(binPath, "--version").Output()
	if err != nil {
		return 0
	}
	return parseChromeVersion(strings.TrimSpace(string(out)))
}

// buildUserAgentMetadata constructs Client Hints metadata from the browser's
// product string (e.g. "HeadlessChrome/131.0.6778.86"). The brands list uses
// real Chrome and Chromium entries — never HeadlessChrome — so that
// navigator.userAgentData.brands and the Sec-CH-UA HTTP header look identical
// to a real headed browser.
func buildUserAgentMetadata(product string) *proto.EmulationUserAgentMetadata {
	major := "131"
	full := "131.0.0.0"
	if m := chromeVersionPattern.FindStringSubmatch(product); m != nil {
		major = m[1]
		full = m[1] + "." + m[2]
	}

	// GREASE-like "Not A;Brand" entry mirrors what real Chrome sends.
	brands := []*proto.EmulationUserAgentBrandVersion{
		{Brand: "Chromium", Version: major},
		{Brand: "Google Chrome", Version: major},
		{Brand: "Not A;Brand", Version: "99"},
	}
	fullVersionList := []*proto.EmulationUserAgentBrandVersion{
		{Brand: "Chromium", Version: full},
		{Brand: "Google Chrome", Version: full},
		{Brand: "Not A;Brand", Version: "99.0.0.0"},
	}

	platform, arch := clientHintsPlatform()

	return &proto.EmulationUserAgentMetadata{
		Brands:          brands,
		FullVersionList: fullVersionList,
		FullVersion:     full,
		Platform:        platform,
		PlatformVersion: "15.0.0",
		Architecture:    arch,
		Model:           "",
		Mobile:          false,
	}
}

// clientHintsPlatform returns the Sec-CH-UA-Platform and Sec-CH-UA-Arch
// values that match the current OS and architecture. These must match
// what a real Chrome installation would report.
func clientHintsPlatform() (platform, arch string) {
	switch runtime.GOOS {
	case "darwin":
		platform = "macOS"
	case "windows":
		platform = "Windows"
	default:
		platform = "Linux"
	}
	switch runtime.GOARCH {
	case "arm64":
		arch = "arm"
	case "amd64":
		arch = "x86"
	default:
		arch = runtime.GOARCH
	}
	return
}

// buildLauncher constructs a fully-configured rod launcher with all stealth
// settings, viewport, profile, and headed/headless mode applied. Both the
// primary Start path and the SingletonLock recovery retry call this so the
// two paths cannot drift — a missing flag in one of them previously meant
// stealth would silently degrade after a stale-lock recovery.
func (e *Executor) buildLauncher() *launcher.Launcher {
	l := launcher.New()

	// Use system Chrome if available, fall back to rod's auto-download.
	if path, exists := launcher.LookPath(); exists {
		l = l.Bin(path)
		// Cache Chrome version on first call so we don't shell out repeatedly.
		if e.chromeVersion == 0 {
			e.chromeVersion = detectChromeVersion(path)
		}
	}

	if e.profileDir != "" {
		l = l.UserDataDir(e.profileDir)
	}

	l = l.Set("disable-blink-features", "AutomationControlled")
	l = l.Delete("enable-automation")

	// Set default viewport via Chrome flags.
	vp := DefaultViewport()
	if e.workflow != nil && e.workflow.Viewport != nil {
		vp = *e.workflow.Viewport
	}
	l = l.Set("window-size", fmt.Sprintf("%d,%d", vp.Width, vp.Height))

	if e.headed {
		l = l.Headless(false)
		l = l.Set("window-position", "100,100")
	} else {
		// Chrome 109+ ships a "new" headless mode that shares the same
		// renderer code path as headed Chrome. It closes a number of
		// fingerprintable differences (window.chrome stub, plugin array,
		// permissions API behavior). Chrome 132+ removes the legacy mode
		// entirely so the flag becomes a no-op there.
		//
		// On Chrome <109 the flag is unrecognized and may cause Chrome to
		// fall back to legacy headless, ignore the flag, or launch headed.
		// We fall back to bare --headless for those old versions.
		if e.chromeVersion > 0 && e.chromeVersion < 109 {
			l = l.Headless(true)
		} else {
			l = l.Set("headless", "new")
		}
	}

	return l
}

// Start launches the browser with stealth settings.
func (e *Executor) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startLocked()
}

func (e *Executor) startLocked() error {
	if e.profileDir != "" {
		os.MkdirAll(e.profileDir, 0755)
	}

	l := e.buildLauncher()

	controlURL, err := l.Launch()
	if err != nil && e.profileDir != "" && strings.Contains(err.Error(), "SingletonLock") {
		if e.removeStaleSingletonLock() {
			// Stale lock removed — retry once with a fresh launcher.
			controlURL, err = e.buildLauncher().Launch()
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

	// Get the real User-Agent from the running browser instead of using the hardcoded fallback.
	var browserUA, browserProduct string
	if ver, err := (proto.BrowserGetVersion{}).Call(browser); err == nil {
		browserUA = ver.UserAgent
		browserProduct = ver.Product
	}
	e.cachedProduct = browserProduct
	e.userAgent = resolveUserAgent(browserUA)
	e.userAgentMeta = buildUserAgentMetadata(browserProduct)

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
	e.closeLocked()
	e.headed = headed
	return e.startLocked()
}

// Close shuts down the browser.
func (e *Executor) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeLocked()
}

func (e *Executor) closeLocked() {
	if e.browser != nil {
		e.browser.Close()
	}
}

// NavigateTo creates a new page, injects stealth, and navigates to the given URL.
// Used by one-shot commands (inspect, screenshot, eval) that don't need a workflow.
func (e *Executor) NavigateTo(url string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
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
	e.refreshUserAgentFromBrowser()
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent:         e.userAgent,
		UserAgentMetadata: e.userAgentMeta,
	})
	if err := page.Navigate(url); err != nil {
		return fmt.Errorf("navigate to %s: %w", url, err)
	}
	page.MustWaitLoad()
	e.captureStatusCode()
	return nil
}

// urlsMatchForSkip reports whether the target URL matches the current page URL
// closely enough to skip navigation. Compares scheme, host, and path exactly.
// Query string and fragment are also compared exactly.
func urlsMatchForSkip(current, target string) bool {
	cur, err1 := url.Parse(current)
	tgt, err2 := url.Parse(target)
	if err1 != nil || err2 != nil {
		return false
	}
	return cur.Scheme == tgt.Scheme &&
		cur.Host == tgt.Host &&
		cur.Path == tgt.Path &&
		cur.RawQuery == tgt.RawQuery &&
		cur.Fragment == tgt.Fragment
}

// setupPage prepares the page for an action: creates or reuses tabs, navigates
// if needed, injects stealth, and sets viewport/UA. Returns an error string or "".
func (e *Executor) setupPage(action Action) string {
	targetURL := ""
	if action.URL != "" {
		targetURL = InterpolateEnv(action.URL, e.workflow.Env)
	}

	// No URL and no existing page — nothing to work with.
	if targetURL == "" && e.page == nil {
		return "action has no URL and no prior page to continue from"
	}

	// Decide whether we can reuse the current page.
	reuseCurrentPage := false
	if targetURL == "" && e.page != nil {
		// No URL specified — operate on the current page (continuation pattern).
		reuseCurrentPage = true
	} else if targetURL != "" && !action.ForceNavigate && e.page != nil {
		// URL specified — check if it matches the already-loaded page.
		// Note: page.Info() may lag after pushState/replaceState. If the page
		// state diverged via SPA navigation, use force_navigate: true.
		if info, err := e.page.Info(); err == nil && urlsMatchForSkip(info.URL, targetURL) {
			reuseCurrentPage = true
		}
	}

	if reuseCurrentPage {
		// Reuse existing page — stealth is already registered via
		// EvalOnNewDocument so no re-injection needed. Update viewport.
		e.setViewport(ResolveViewport(e.workflow.Viewport, action.Viewport))
		return ""
	}

	// Close the previous page to prevent tab accumulation across actions.
	if e.page != nil {
		e.page.Close()
	}

	// Need a new page.
	page, err := e.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return fmt.Sprintf("create page: %v", err)
	}
	e.page = page
	e.injectStealth()
	e.setViewport(ResolveViewport(e.workflow.Viewport, action.Viewport))
	e.refreshUserAgentFromBrowser()
	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent:         e.userAgent,
		UserAgentMetadata: e.userAgentMeta,
	})

	// Navigate if a URL was specified.
	if targetURL != "" {
		if err := page.Navigate(targetURL); err != nil {
			return fmt.Sprintf("navigate to %s: %v", targetURL, err)
		}
		page.MustWaitLoad()
		e.captureStatusCode()
	}

	return ""
}

// RunAction executes a named action from the workflow.
// Returns a structured ActionResult suitable for JSON serialization.
func (e *Executor) RunAction(name string) *ActionResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	start := time.Now()
	action, ok := e.workflow.Actions[name]
	if !ok {
		return &ActionResult{
			OK:     false,
			Action: name,
			Error:  fmt.Sprintf("action %q not found in workflow %q", name, e.workflow.Name),
		}
	}

	// Set up the page: reuse current tab or create a new one.
	if errMsg := e.setupPage(action); errMsg != "" {
		return &ActionResult{
			OK:         false,
			Action:     name,
			DurationMs: time.Since(start).Milliseconds(),
			Error:      errMsg,
		}
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
			// e.page is nil after restart — setupPage will create a fresh tab.
			if errMsg := e.setupPage(action); errMsg != "" {
				failResult.Error += fmt.Sprintf("; headed setup failed: %s", errMsg)
				failResult.DurationMs = time.Since(start).Milliseconds()
				return failResult
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

		// Capture before-screenshot in memory (ring buffer of one).
		// Zero disk I/O on success. Only written to disk on failure.
		var beforeData []byte
		if debugScreenshotsEnabled(e.workflow.DebugScreenshots) && e.page != nil {
			beforeData = e.captureJPEG()
		}

		if err := e.executeStep(step); err != nil {
			if step.Optional {
				if e.debug {
					log.Printf("[%s] optional step %d failed (non-fatal): %v", name, i+1, err)
				}
				continue
			}

			screenshotPath := fmt.Sprintf("%s_failed_%s_%d.png", name, time.Now().Format("20060102-150405"), i)
			e.takeScreenshot(screenshotPath)

			// Write the before screenshot to disk now that we know it's needed
			var beforePath string
			if len(beforeData) > 0 {
				beforePath = filepath.Join(os.TempDir(), fmt.Sprintf("%s_before_%d.jpg", name, i))
				os.WriteFile(beforePath, beforeData, 0644)
			}

			result := &ActionResult{
				OK:               false,
				Action:           name,
				Steps:            i,
				Error:            fmt.Sprintf("action %q, %s: %v", name, label, err),
				FailedStep:       i + 1,
				StepType:         stepType(step),
				Screenshot:       filepath.Join(os.TempDir(), screenshotPath),
				ScreenshotBefore: beforePath,
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
	// Single deadline shared between element lookup and disabled retry
	// so the total wait never exceeds the user-specified timeout.
	deadline := time.Now().Add(timeout)

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

	// Retry within the shared deadline — the element may start disabled and
	// become enabled after async option loading (e.g., TCGplayer's AJAX-populated dropdowns).
	pollInterval := 500 * time.Millisecond
	for {
		res, err := el.Eval(js, value, text)
		if err != nil {
			return fmt.Errorf("select eval: %w", err)
		}

		result := res.Value.Str()
		switch {
		case result == "ok":
			return nil
		case result == "disabled":
			if time.Now().After(deadline) {
				return fmt.Errorf("select %q is still disabled after %s", s.Selector, timeout)
			}
			time.Sleep(pollInterval)
			continue
		case len(result) > 16 && result[:16] == "not_found_text:":
			return fmt.Errorf("no option with text %q in %q", result[16:], s.Selector)
		case len(result) > 17 && result[:17] == "not_found_value:":
			return fmt.Errorf("no option with value %q in %q", result[17:], s.Selector)
		default:
			return fmt.Errorf("select failed: %s", result)
		}
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
	e.page.MustEvalOnNewDocument(`
		Object.defineProperty(navigator, 'webdriver', {get: () => undefined, configurable: true});
	`)
}

// debugScreenshotsEnabled returns whether before/after debug screenshots are
// enabled. Default is true (nil means enabled).
func debugScreenshotsEnabled(setting *bool) bool {
	if setting == nil {
		return true
	}
	return *setting
}

// captureJPEG returns a lightweight JPEG screenshot as bytes.
// Returns nil if the page is unavailable or screenshot fails.
func (e *Executor) captureJPEG() []byte {
	if e.page == nil {
		return nil
	}
	quality := 50
	data, err := e.page.Screenshot(true, &proto.PageCaptureScreenshot{
		Format:  proto.PageCaptureScreenshotFormatJpeg,
		Quality: &quality,
	})
	if err != nil {
		return nil
	}
	return data
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
	e.mu.Lock()
	headed := e.headed
	page := e.page
	e.mu.Unlock()
	if !headed || page == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "\nBrowser kept open for debugging. Press Enter to close...\n")
	buf := make([]byte, 1)
	os.Stdin.Read(buf)
}

// Page returns the current page for advanced usage.
func (e *Executor) Page() *rod.Page {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.page
}

// KeyPress sends a keyboard input to the current page.
func (e *Executor) KeyPress(key input.Key) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.page.Keyboard.Press(key)
}

// IsHeaded returns whether the browser is in headed (visible) mode.
func (e *Executor) IsHeaded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.headed
}

// SetEnv sets an environment variable in the workflow's env map.
// These are used by InterpolateEnv for ${VAR} substitution in step values.
func (e *Executor) SetEnv(key, value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.workflow.Env == nil {
		e.workflow.Env = make(map[string]string)
	}
	e.workflow.Env[key] = value
}
