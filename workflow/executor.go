package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	"time"

	"github.com/crackfetch/brainstorm/internal/events"
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
	// pendingDownloadURL captures the page URL right before the click that
	// triggers a download. download.return_to=previous restores this URL once
	// the file is captured (a click-triggered download leaves the tab at
	// about:blank, which breaks subsequent wait_url and interaction steps).
	pendingDownloadURL string

	// loginURL is the URL to open for manual login before connecting CDP.
	// When set, Start() launches Chrome headed with this URL but does NOT
	// connect CDP. Call ConnectAfterLogin() after the user logs in.
	loginURL string
	// loginSuccessURL is a substring the URL must contain after login.
	loginSuccessURL string
	// controlURL is the CDP WebSocket URL, stored between Start() and
	// ConnectAfterLogin() when loginURL is set.
	controlURL string
	// debugPort is the actual port Chrome bound to when launched with
	// --remote-debugging-port=0. Set by launchForLogin() after Chrome starts.
	debugPort string

	// announceWriter receives status lines (currently just the headed-launch
	// announcement). Nil falls back to os.Stderr. Set only by tests via
	// setAnnounceWriterForTest in export_test.go — not part of the public API,
	// so callers can't install a writer that blocks while e.mu is held.
	announceWriter io.Writer

	// pendingAnnounce* hold launch info captured under e.mu in startLocked /
	// launchForLogin. Start() flushes them after releasing the lock so the
	// I/O does not stall any concurrent executor method that contends on e.mu.
	pendingAnnouncePID  int
	pendingAnnounceExe  string
	pendingAnnounceFlag bool

	// events receives lifecycle events (step_start/step_end/retry_attempt/...).
	// Default is events.Nop, so unset emitters cost a single inlinable method
	// call. Set via WithEventEmitter. Never nil after NewExecutor.
	events events.Emitter

	// observer, when non-nil, receives a callback per executed step. Used
	// by site-drift detection to record selector hits / text hashes. Its
	// hooks must not block — see workflow/observer.go for the contract.
	observer StepObserver
}

// NewExecutor creates an executor with browser configuration.
// Use functional options: WithHeaded(true), WithDebug(true), WithProfileDir("...").
func NewExecutor(w *Workflow, opts ...Option) *Executor {
	e := &Executor{workflow: w, userAgent: fallbackUserAgent, events: events.Nop{}}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// emit publishes an event, defaulting to Nop when no emitter has been
// installed (tests sometimes construct &Executor{} directly, bypassing
// NewExecutor's default).
func (e *Executor) emit(ev events.Event) {
	if e.events == nil {
		return
	}
	e.events.Emit(ev)
}

// stepTarget extracts a human-readable target string for a step (selector,
// URL, text, etc). Used to populate Event.Target.
func stepTarget(s Step) string {
	switch {
	case s.Navigate != "":
		return s.Navigate
	case s.Click != nil:
		if s.Click.Selector != "" {
			return s.Click.Selector
		}
		return s.Click.Text
	case s.Fill != nil:
		return s.Fill.Selector
	case s.Select != nil:
		return s.Select.Selector
	case s.Upload != nil:
		return s.Upload.Selector
	case s.WaitVisible != nil:
		return s.WaitVisible.Selector
	case s.WaitText != nil:
		return s.WaitText.Text
	case s.WaitURL != nil:
		return s.WaitURL.Match
	case s.WaitEnabled != nil:
		return s.WaitEnabled.Selector
	case s.Screenshot != "":
		return s.Screenshot
	}
	return ""
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
	l := launcher.New().Leakless(false)

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
//
// The headed-launch announcement (a single stderr line with PID + exe path)
// is emitted AFTER releasing e.mu so the writer call cannot block other
// executor methods. The pending data is captured under the lock; only the
// I/O happens unlocked.
func (e *Executor) Start() error {
	e.mu.Lock()
	err := e.startLocked()
	pid, exe, announce := e.pendingAnnouncePID, e.pendingAnnounceExe, e.pendingAnnounceFlag
	e.pendingAnnouncePID, e.pendingAnnounceExe, e.pendingAnnounceFlag = 0, "", false
	e.mu.Unlock()

	if announce {
		e.announceHeadedLaunch(pid, exe)
		e.surfaceMacOSWindow(exe)
	}
	return err
}

func (e *Executor) startLocked() error {
	if e.profileDir != "" {
		os.MkdirAll(e.profileDir, 0755)
	}

	l := e.buildLauncher()

	// When loginURL is set, launch Chrome directly (not via rod's launcher)
	// to avoid both CDP detection and "Opening in existing browser session"
	// issues. Rod's launcher is only used later in ConnectAfterLogin().
	if e.loginURL != "" {
		return e.launchForLogin(l)
	}

	controlURL, err := l.Launch()
	if err != nil && e.profileDir != "" && strings.Contains(err.Error(), "SingletonLock") {
		if e.removeStaleSingletonLock() {
			// Stale lock removed — retry once with a fresh launcher.
			l = e.buildLauncher()
			controlURL, err = l.Launch()
		}
	}
	if err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}

	// Capture launch info for the post-unlock announcement. Surface the
	// launch on stderr so a user/LLM driving brz knows the window exists —
	// macOS in particular tends to open new Chromium instances behind
	// whatever app currently owns the foreground.
	if e.headed {
		e.pendingAnnouncePID = l.PID()
		e.pendingAnnounceExe = l.Get("rod-bin")
		e.pendingAnnounceFlag = true
	}

	// Delayed connection: store the control URL and return without
	// connecting CDP. Chrome is running with the login page open.
	if e.loginURL != "" {
		e.controlURL = controlURL
		return nil
	}

	return e.connectCDP(controlURL)
}

// launchForLogin starts Chrome directly (bypassing rod's launcher) with the
// login URL and a remote debugging port. This avoids two problems:
// 1. Rod's launcher connects CDP immediately, which triggers bot detection
// 2. On macOS, Chrome may attach to an existing instance instead of opening new
//
// Chrome is launched with --remote-debugging-port=0 so the OS assigns a free
// port, eliminating collisions with other Chrome instances. The actual port is
// discovered from the DevToolsActivePort file Chrome writes to profileDir.
//
// The caller must invoke ConnectAfterLogin() after the user logs in.
func (e *Executor) launchForLogin(l *launcher.Launcher) error {
	// Find the Chrome binary path
	bin := l.Get("rod-bin")
	if bin == "" {
		if path, exists := launcher.LookPath(); exists {
			bin = path
		}
	}
	if bin == "" {
		return fmt.Errorf("Chrome not found — install Google Chrome")
	}

	if e.profileDir == "" {
		return fmt.Errorf("profileDir is required when using WithLoginURL (needed for debug port discovery)")
	}

	args := []string{
		"--remote-debugging-port=0", // OS assigns a free port; actual port read from DevToolsActivePort
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-blink-features=AutomationControlled",
	}
	// Linux CI/container environments require --no-sandbox to start Chrome at
	// all. Without it Chrome crashes before binding the debug port, so
	// DevToolsActivePort is never written. --disable-dev-shm-usage prevents
	// crashes caused by /dev/shm being too small in Docker/GitHub Actions.
	if runtime.GOOS == "linux" {
		args = append(args, "--no-sandbox", "--disable-dev-shm-usage")
	}
	absDir, _ := filepath.Abs(e.profileDir)
	args = append(args, "--user-data-dir="+absDir)

	// Stale-port-race guard (#41): delete any DevToolsActivePort left behind
	// by a previous Chrome run against this profile dir. Chrome only writes
	// this file *after* it binds the new --remote-debugging-port=0 port, so
	// absence is an unambiguous "not ready yet" signal. Without this, a fast
	// relaunch can land discoverDebugPort on the previous run's port number
	// before Chrome rewrites the file — silent connection-refused later.
	_ = os.Remove(filepath.Join(absDir, "DevToolsActivePort"))
	// Belt-and-suspenders: even if the Remove above failed (rare — file
	// open by another process, perm error), capture the wall-clock right
	// before launch so discoverDebugPort can reject reads whose mtime
	// hasn't advanced past it.
	preLaunch := time.Now()

	vp := DefaultViewport()
	if e.workflow != nil && e.workflow.Viewport != nil {
		vp = *e.workflow.Viewport
	}
	args = append(args, fmt.Sprintf("--window-size=%d,%d", vp.Width, vp.Height))
	args = append(args, "--window-position=100,100")
	// Use --app= instead of a bare URL argument. When Chrome sees a bare URL
	// and another Chrome instance is already running, Windows Chrome delegates
	// to the existing instance via a named pipe and exits with status 0. The
	// --app= flag makes Chrome treat this as an app-mode launch, which bypasses
	// the singleton delegation and starts a fully independent instance with its
	// own debug port. The trade-off is no address bar, which the agent doesn't need.
	args = append(args, "--app="+e.loginURL)

	cmd := exec.Command(bin, args...)
	cmd.Stdout = io.Discard
	// Always capture stderr so we can report Chrome's error output if it
	// crashes during startup. In debug mode, tee to os.Stderr as well.
	var stderrBuf bytes.Buffer
	if e.debug {
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	} else {
		cmd.Stderr = &stderrBuf
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch Chrome for login: %w", err)
	}

	// launchForLogin always opens a visible window — capture launch info
	// for the post-unlock announcement (Start releases e.mu before flushing).
	e.pendingAnnouncePID = cmd.Process.Pid
	e.pendingAnnounceExe = bin
	e.pendingAnnounceFlag = true

	// Monitor Chrome process in a goroutine so we can detect early crashes.
	chromeDone := make(chan error, 1)
	go func() { chromeDone <- cmd.Wait() }()

	// Discover the actual port Chrome bound to. Chrome writes this to
	// <profileDir>/DevToolsActivePort after binding. We poll for up to 15s
	// (Windows can be slow due to antivirus scanning or cold-start delays).
	timeout := 5 * time.Second
	if runtime.GOOS == "windows" {
		timeout = 15 * time.Second
	}
	port, err := discoverDebugPort(absDir, timeout, chromeDone, preLaunch)
	if err != nil {
		// Include Chrome's stderr in the error if available.
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			// Cap to avoid huge error messages.
			if len(stderr) > 500 {
				stderr = stderr[:500] + "..."
			}
			err = fmt.Errorf("%w\nChrome stderr: %s", err, stderr)
		}
		killProcessTree(cmd.Process)
		return fmt.Errorf("discover debug port: %w", err)
	}

	e.debugPort = port
	e.controlURL = "ws://127.0.0.1:" + port
	return nil
}

// discoverDebugPort polls Chrome's DevToolsActivePort file until the actual
// debug port appears. Chrome writes this file after binding to the OS-assigned
// port when launched with --remote-debugging-port=0.
//
// If chromeDone is non-nil it is checked each iteration so we can fail fast
// when Chrome crashes during startup instead of waiting the full timeout.
//
// staleBefore is the wall-clock captured immediately before Chrome was
// launched. Any DevToolsActivePort whose mtime is ≤ staleBefore is treated
// as a leftover from a previous Chrome run (the stale-port race in #41) and
// ignored. Pass the zero time (time.Time{}) to disable this gate.
//
// File format: "<port>\n<ws-path>". Only the first line (port number) is used.
func discoverDebugPort(profileDir string, timeout time.Duration, chromeDone <-chan error, staleBefore time.Time) (string, error) {
	portFile := filepath.Join(profileDir, "DevToolsActivePort")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if Chrome crashed before we even got the port file.
		if chromeDone != nil {
			select {
			case waitErr := <-chromeDone:
				if waitErr != nil {
					return "", fmt.Errorf("Chrome exited during startup: %w", waitErr)
				}
				return "", fmt.Errorf("Chrome exited during startup with status 0 (unexpected)")
			default:
				// Still running — continue polling.
			}
		}

		// Stat first so we can reject stale files (mtime ≤ staleBefore) without
		// even reading them. Chrome rewrites the file on bind, so a fresh write
		// has a fresh mtime.
		if !staleBefore.IsZero() {
			info, statErr := os.Stat(portFile)
			if statErr != nil || !info.ModTime().After(staleBefore) {
				time.Sleep(100 * time.Millisecond)
				continue
			}
		}

		data, err := os.ReadFile(portFile)
		if err == nil && len(data) > 0 {
			line := strings.SplitN(string(data), "\n", 2)[0]
			line = strings.TrimSpace(line)
			if port, err := strconv.Atoi(line); err == nil && port > 0 {
				return line, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Provide diagnostic detail: did the file never appear, or did it contain bad data?
	data, readErr := os.ReadFile(portFile)
	if readErr != nil {
		return "", fmt.Errorf("Chrome debug port not found after %s — DevToolsActivePort was never written to %s (check Chrome started correctly)", timeout, profileDir)
	}
	return "", fmt.Errorf("Chrome debug port not found after %s — DevToolsActivePort exists but contains invalid data: %q", timeout, string(data))
}

// connectCDP establishes the CDP connection to a running Chrome.
func (e *Executor) connectCDP(controlURL string) error {
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

// NeedsLogin returns true if the executor was configured with WithLoginURL
// and CDP has not yet been connected.
func (e *Executor) NeedsLogin() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loginURL != "" && e.browser == nil && e.controlURL != ""
}

// LoginURL returns the login URL configured via WithLoginURL, or empty string.
func (e *Executor) LoginURL() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loginURL
}

// DebugPort returns the actual CDP debug port Chrome is listening on.
// Only valid after Start() has been called with WithLoginURL and Chrome has started.
func (e *Executor) DebugPort() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.debugPort
}

// LoginSuccessURL returns the success URL substring configured via WithLoginURL.
func (e *Executor) LoginSuccessURL() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loginSuccessURL
}

// ConnectAfterLogin establishes the CDP connection to Chrome that was launched
// by Start() with WithLoginURL. Call this after the user has completed login.
// After this returns, the executor is fully functional for RunAction() etc.
func (e *Executor) ConnectAfterLogin() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.controlURL == "" {
		return fmt.Errorf("no pending login session — call Start() with WithLoginURL first")
	}
	if e.browser != nil {
		return nil // already connected
	}

	// Resolve the WebSocket URL from the debug port's HTTP endpoint
	wsURL, err := launcher.ResolveURL(strings.TrimPrefix(e.controlURL, "ws://"))
	if err != nil {
		return fmt.Errorf("resolve Chrome debug URL: %w", err)
	}

	if err := e.connectCDP(wsURL); err != nil {
		return err
	}

	// Clear loginURL so subsequent restart() calls use normal CDP flow.
	// The session cookies are now in Chrome's memory — no need to re-login
	// unless the Chrome process dies.
	e.loginURL = ""
	e.controlURL = ""
	return nil
}

// removeStaleSingletonLock checks whether the SingletonLock in the profile
// directory is held by a dead process. If so, it removes the lock file and
// returns true. If a live process holds the lock, it returns false — we never
// kill another process's Chrome.
//
// On Linux/macOS, SingletonLock is a symlink whose target encodes "hostname-pid".
// On Windows, it is a regular file containing the PID as plain text.
func (e *Executor) removeStaleSingletonLock() bool {
	lockPath := filepath.Join(e.profileDir, "SingletonLock")

	// Try symlink first (Linux/macOS).
	target, err := os.Readlink(lockPath)
	if err != nil {
		// Not a symlink — on Windows, SingletonLock is a regular file.
		// Try reading PID from file contents.
		data, readErr := os.ReadFile(lockPath)
		if readErr != nil {
			// Doesn't exist or can't read — try removing anyway.
			if os.Remove(lockPath) == nil {
				log.Printf("Removed stale SingletonLock (unreadable)")
				return true
			}
			return false
		}
		// Try parsing PID directly from file contents.
		pidStr := strings.TrimSpace(string(data))
		if pid, parseErr := strconv.Atoi(pidStr); parseErr == nil {
			if isProcessAlive(pid) {
				return false // Chrome is still running
			}
		}
		// Process is dead or PID unparseable — remove the lock.
		if os.Remove(lockPath) == nil {
			log.Printf("Removed stale SingletonLock (owner process is dead)")
			return true
		}
		return false
	}

	// Symlink path (Linux/macOS) — parse PID from "hostname-pid" format.
	parts := strings.SplitN(target, "-", 2)
	if len(parts) == 2 {
		if pid, err := strconv.Atoi(parts[1]); err == nil {
			if isProcessAlive(pid) {
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
// RunAction is the public entry: locks e.mu, then dispatches to the
// internal runActionLocked. The internal form lets on_error recovery
// invoke another action without re-acquiring the lock (which would
// deadlock since we don't release between primary and recovery).
func (e *Executor) RunAction(name string) *ActionResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	wfName := ""
	if e.workflow != nil {
		wfName = e.workflow.Name
	}
	start := time.Now()
	e.emit(events.Event{Event: events.ActionStart, Workflow: wfName, Action: name})
	r := e.runActionLocked(name, 0)
	status := events.StatusOK
	errMsg := ""
	if r != nil && !r.OK {
		status = events.StatusError
		errMsg = r.Error
	}
	e.emit(events.Event{
		Event:      events.ActionEnd,
		Workflow:   wfName,
		Action:     name,
		Status:     status,
		DurationMs: time.Since(start).Milliseconds(),
		Error:      errMsg,
	})
	return r
}

// runActionLocked is the internal action runner. depth tracks recursion
// from on_error so we can refuse infinitely-chained recoveries.
func (e *Executor) runActionLocked(name string, depth int) *ActionResult {
	const maxOnErrorDepth = 1
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
				return e.maybeRunOnError(name, action, failResult, depth)
			}
			// Surface the headed-launch announcement that restart's startLocked
			// stashed. We hold e.mu here; the announce writes to os.Stderr by
			// default (non-blocking). Tests inject only synchronous in-memory
			// buffers, never anything that could deadlock under the lock.
			if e.pendingAnnounceFlag {
				pid, exe := e.pendingAnnouncePID, e.pendingAnnounceExe
				e.pendingAnnouncePID, e.pendingAnnounceExe, e.pendingAnnounceFlag = 0, "", false
				e.announceHeadedLaunch(pid, exe)
				e.surfaceMacOSWindow(exe)
			}
			// e.page is nil after restart — setupPage will create a fresh tab.
			if errMsg := e.setupPage(action); errMsg != "" {
				failResult.Error += fmt.Sprintf("; headed setup failed: %s", errMsg)
				failResult.DurationMs = time.Since(start).Milliseconds()
				return e.maybeRunOnError(name, action, failResult, depth)
			}
			// Retry all steps in headed mode.
			if retryResult := e.runSteps(name, action); retryResult != nil {
				retryResult.Escalated = true
				retryResult.DurationMs = time.Since(start).Milliseconds()
				return e.maybeRunOnError(name, action, retryResult, depth)
			}
			escalated = true
		} else {
			failResult.DurationMs = time.Since(start).Milliseconds()
			return e.maybeRunOnError(name, action, failResult, depth)
		}
	}
	_ = maxOnErrorDepth // const referenced via maybeRunOnError; keeps it visible at call-site

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
			// Eval-assertion failure is a terminal action failure, so
			// the on_error recovery hook fires here too. Without this,
			// post-action assertions could veto a step-success-only
			// outcome and skip the recovery the user configured.
			return e.maybeRunOnError(name, action, result, depth)
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

		stepKind := stepType(step)
		stepStart := time.Now()
		e.emit(events.Event{
			Event:    events.StepStart,
			Action:   name,
			Step:     label,
			StepNum:  i + 1,
			StepType: stepKind,
			Target:   stepTarget(step),
		})

		// Look ahead: if the NEXT step is a download, register WaitDownload now
		// (before executing the current click step) and capture the current URL
		// so download.return_to=previous can restore it after the download lands.
		if i+1 < len(action.Steps) && action.Steps[i+1].Download != nil {
			if step.Click != nil {
				downloadDir := filepath.Join(os.TempDir(), "brz-downloads")
				os.MkdirAll(downloadDir, 0755)
				e.pendingDownloadWait = e.browser.WaitDownload(downloadDir)
				e.pendingDownloadDir = downloadDir
				if info, err := e.page.Info(); err == nil {
					e.pendingDownloadURL = info.URL
				}
				if e.debug {
					log.Printf("[%s] pre-registered WaitDownload before click (return URL=%q)",
						name, e.pendingDownloadURL)
				}
			}
		}

		// Capture before-screenshot in memory (ring buffer of one).
		// Zero disk I/O on success. Only written to disk on failure.
		var beforeData []byte
		if debugScreenshotsEnabled(e.workflow.DebugScreenshots) && e.page != nil {
			beforeData = e.captureJPEG()
		}

		stepErr := e.executeStepWithRetry(name, step, i+1)
		// Notify the optional observer regardless of step outcome — drift
		// detection wants to record matched_count even on failed steps so
		// "0 matches today (was 12)" surfaces rather than being swallowed.
		if e.observer != nil {
			e.observer.OnStep(i, step, stepType(step), stepErr == nil, e.page)
		}
		if err := stepErr; err != nil {
			if step.Optional {
				if e.debug {
					log.Printf("[%s] optional step %d failed (non-fatal): %v", name, i+1, err)
				}
				e.emit(events.Event{
					Event:      events.StepEnd,
					Action:     name,
					Step:       label,
					StepNum:    i + 1,
					StepType:   stepKind,
					Status:     events.StatusSkipped,
					DurationMs: time.Since(stepStart).Milliseconds(),
					Error:      err.Error(),
				})
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

			// Capture similar elements for agent context (helps avoid re-inspect round-trip).
			if selector := StepSelector(step); selector != "" && e.page != nil {
				similarSelector := SimilarElementsSelectorForStepSelector(selector)
				if elements := e.captureSimilarElements(similarSelector); len(elements) > 0 {
					result.PageElements = elements
					// Surface the candidates in the error string itself so a
					// human (or LLM) reading just the error sees actionable
					// suggestions without having to inspect the JSON body.
					// The full list stays on result.PageElements for any
					// programmatic consumer.
					if hint := summarizeNearbyElements(elements, 5); hint != "" {
						result.Error += "\n  " + hint
					}
				}
			}

			e.emit(events.Event{
				Event:      events.StepEnd,
				Action:     name,
				Step:       label,
				StepNum:    i + 1,
				StepType:   stepKind,
				Status:     events.StatusError,
				DurationMs: time.Since(stepStart).Milliseconds(),
				Error:      err.Error(),
			})
			return result
		}
		e.emit(events.Event{
			Event:      events.StepEnd,
			Action:     name,
			Step:       label,
			StepNum:    i + 1,
			StepType:   stepKind,
			Status:     events.StatusOK,
			DurationMs: time.Since(stepStart).Milliseconds(),
		})
	}
	return nil
}

// maybeRunOnError is the terminal-failure recovery hook. When action.OnError
// names another action and we haven't already exceeded the recovery-chain
// depth, run the recovery via runActionLocked under the existing e.mu.
//
// Results:
//   - No on_error set: failResult passes through unchanged.
//   - Recovery action missing: append a clear error, return failResult.
//   - Recovery succeeds: original failResult.OK stays false, but Error gets
//     a "; on_error 'X' recovery succeeded" suffix so consumers see the
//     recovery ran. ok stays false because the originally-requested action
//     did fail; cleanup-worked doesn't change that fact.
//   - Recovery itself fails: append the recovery's error to the original.
//   - Depth check: at most 1 level of on_error chaining. Recovery actions
//     can't have their own on_error (would silently mask deeper failures
//     and risk infinite recursion).
func (e *Executor) maybeRunOnError(name string, action Action, failResult *ActionResult, depth int) *ActionResult {
	const maxOnErrorDepth = 1

	// Clear any stale pre-registered WaitDownload from the failed
	// primary. If we don't, a recovery action that itself runs a
	// click+download would (a) not get a fresh look-ahead registration
	// because the next-step-is-download check already fired, AND (b)
	// could observe the stale wait in doDownload's pre-registered path,
	// confusing the file routing. Cheapest fix: nil it here so any
	// subsequent action starts from a clean slate.
	e.pendingDownloadWait = nil
	e.pendingDownloadDir = ""
	e.pendingDownloadURL = ""

	if action.OnError == "" {
		return failResult
	}
	if depth >= maxOnErrorDepth {
		failResult.Error += fmt.Sprintf("; on_error chain limit hit at depth %d (recovery actions cannot themselves declare on_error)", depth)
		return failResult
	}
	if _, ok := e.workflow.Actions[action.OnError]; !ok {
		failResult.Error += fmt.Sprintf("; on_error action %q not found in workflow", action.OnError)
		return failResult
	}
	if e.debug {
		log.Printf("[%s] running on_error recovery: %s", name, action.OnError)
	}
	recoveryResult := e.runActionLocked(action.OnError, depth+1)
	if recoveryResult == nil {
		// Defensive: runActionLocked is documented to always return non-nil.
		// If something changes, fall back to the original failure unmodified.
		return failResult
	}
	if recoveryResult.OK {
		failResult.Error += fmt.Sprintf("; on_error '%s' recovery succeeded", action.OnError)
	} else {
		failResult.Error += fmt.Sprintf("; on_error '%s' also failed: %s", action.OnError, recoveryResult.Error)
	}
	return failResult
}

// executeStepWithRetry runs a step, optionally retrying on failure when
// step.Retry is configured. Retry attempts run AFTER the initial attempt,
// so retry.count = 3 means 1 initial + 2 retries = 3 total attempts.
//
// Backoff strategies:
//   "" / "none"     no delay between attempts
//   "linear"        attempt N waits initial * N
//   "exponential"   attempt N waits initial * 2^N
//
// Notes on retry + downloads: the click+download look-ahead pre-registers
// WaitDownload before the click step. Retrying the click is safe — the
// pre-registration persists across attempts (we only consume it when
// doDownload runs). Retry on a download step itself is effectively a
// no-op since the triggering click already fired; use action-level
// on_error for recovery in that case.
func (e *Executor) executeStepWithRetry(actionName string, step Step, stepNum int) error {
	err := e.executeStep(step)
	if err == nil {
		return nil
	}
	if step.Retry == nil || step.Retry.Count <= 1 {
		return err
	}

	initial := 1 * time.Second
	if step.Retry.InitialDelay != "" {
		initial = ParseTimeout(step.Retry.InitialDelay)
	}

	for attempt := 1; attempt < step.Retry.Count; attempt++ {
		delay := computeBackoff(step.Retry.Backoff, initial, attempt)
		if delay > 0 {
			time.Sleep(delay)
		}
		if e.debug {
			log.Printf("[%s] retry step %d attempt %d/%d (waited %s, last err: %v)",
				actionName, stepNum, attempt+1, step.Retry.Count, delay, err)
		}
		e.emit(events.Event{
			Event:            events.RetryAttempt,
			Action:           actionName,
			StepNum:          stepNum,
			StepType:         stepType(step),
			Attempt:          attempt + 1,
			RetriesRemaining: step.Retry.Count - attempt - 1,
			Error:            err.Error(),
		})
		err = e.executeStep(step)
		if err == nil {
			if e.debug {
				log.Printf("[%s] retry step %d succeeded on attempt %d", actionName, stepNum, attempt+1)
			}
			return nil
		}
	}
	return fmt.Errorf("step failed after %d attempts: %w", step.Retry.Count, err)
}

// computeBackoff returns the delay before retry attempt N (1-indexed).
// "linear" with attempt=1 returns initial; with attempt=2 returns 2*initial.
// "exponential" with attempt=1 returns 2*initial; with attempt=2 returns 4*initial.
// (Both grow from "initial" at attempt 1, not 0 — the previous attempt
// just failed, so we always wait at least once.)
func computeBackoff(strategy string, initial time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	switch strategy {
	case "linear":
		return initial * time.Duration(attempt)
	case "exponential":
		return initial * time.Duration(1<<attempt)
	case "", "none":
		return 0
	default:
		// Unknown strategy: fall back to linear so a typo doesn't surface
		// as "all retries fired instantly without backoff." Document the
		// supported values; any unknown one is treated as linear.
		return initial * time.Duration(attempt)
	}
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
	case s.WaitEnabled != nil:
		return "wait_enabled"
	case s.Handoff != nil:
		return "handoff"
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

	case step.WaitEnabled != nil:
		return e.doWaitEnabled(step.WaitEnabled)

	case step.Handoff != nil:
		return e.doHandoff(step.Handoff)

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

	// Fast path: no filters, no nth indexing — preserves the original
	// single-Element() lookup for the most common click case. Behavior
	// for existing workflows is unchanged.
	if c.Text == "" && !c.Visible && c.Nth == 0 {
		el, err := e.page.Timeout(timeout).Element(selector)
		if err != nil {
			return fmt.Errorf("find element %s: %w", formatSelectorForError(selector, c.AliasName, e.workflow), err)
		}
		return el.Click(proto.InputMouseButtonLeft, 1)
	}

	// Multi-match path: poll until at least one element matches all filters
	// (selector, then optional Visible filter, then optional Text filter),
	// then resolve Nth (negative indices count from the end).
	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond
	var lastSeen int
	for {
		els, err := e.page.Elements(selector)
		if err == nil {
			lastSeen = len(els)
			if c.Visible {
				els = filterVisibleElements(els)
			}
			if c.Text != "" {
				els = filterElementsByText(els, c.Text)
			}
			if len(els) > 0 {
				idx := c.Nth
				if idx < 0 {
					idx = len(els) + idx
				}
				if idx < 0 || idx >= len(els) {
					return fmt.Errorf("selector %s: nth=%d but only %d match(es) after filters (visible=%v, text=%q)",
						formatSelectorForError(selector, c.AliasName, e.workflow), c.Nth, len(els), c.Visible, c.Text)
				}
				return els[idx].Click(proto.InputMouseButtonLeft, 1)
			}
		}
		if time.Now().After(deadline) {
			selDesc := formatSelectorForError(selector, c.AliasName, e.workflow)
			switch {
			case c.Text != "" && c.Visible:
				return fmt.Errorf("no visible element matching %s with text %q within %s (saw %d raw selector match(es))",
					selDesc, c.Text, timeout, lastSeen)
			case c.Text != "":
				return fmt.Errorf("no element matching %s with text %q within %s", selDesc, c.Text, timeout)
			case c.Visible:
				return fmt.Errorf("no visible element matching %s within %s (saw %d raw selector match(es))",
					selDesc, timeout, lastSeen)
			default:
				return fmt.Errorf("no element matching %s within %s", selDesc, timeout)
			}
		}
		time.Sleep(pollInterval)
	}
}

// filterVisibleElements keeps only elements that the browser would consider
// laid out and renderable: offsetParent != null and a non-zero bounding rect.
// This is the same heuristic typically used in interactive devtools-driven
// inspection, so a click step's "visible: true" matches what a human looking
// at the page would call visible.
func filterVisibleElements(els []*rod.Element) []*rod.Element {
	out := make([]*rod.Element, 0, len(els))
	for _, el := range els {
		res, err := el.Eval(`function() {
			const r = this.getBoundingClientRect();
			return this.offsetParent !== null && r.width > 0 && r.height > 0;
		}`)
		if err == nil && res.Value.Bool() {
			out = append(out, el)
		}
	}
	return out
}

// filterElementsByText returns elements whose .Text() contains the substring.
// Mirrors the existing single-match text behavior so callers using only
// click.text get identical filtering to before.
func filterElementsByText(els []*rod.Element, needle string) []*rod.Element {
	out := make([]*rod.Element, 0, len(els))
	for _, el := range els {
		text, err := el.Text()
		if err == nil && strings.Contains(text, needle) {
			out = append(out, el)
		}
	}
	return out
}

func (e *Executor) doFill(f *FillStep) error {
	el, err := e.page.Timeout(30 * time.Second).Element(f.Selector)
	if err != nil {
		return fmt.Errorf("find element %s: %w", formatSelectorForError(f.Selector, f.AliasName, e.workflow), err)
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
		return fmt.Errorf("find element %s: %w", formatSelectorForError(s.Selector, s.AliasName, e.workflow), err)
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
	// become enabled after async option loading (e.g., AJAX-populated dropdowns).
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
				return fmt.Errorf("select %s is still disabled after %s", formatSelectorForError(s.Selector, s.AliasName, e.workflow), timeout)
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
		return fmt.Errorf("find file input %s: %w", formatSelectorForError(u.Selector, u.AliasName, e.workflow), err)
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
	var preDownloadURL string

	if e.pendingDownloadWait != nil {
		// Use the pre-registered wait (set up before the triggering click).
		wait = e.pendingDownloadWait
		downloadDir = e.pendingDownloadDir
		preDownloadURL = e.pendingDownloadURL
		e.pendingDownloadWait = nil
		e.pendingDownloadDir = ""
		e.pendingDownloadURL = ""
	} else {
		// Fallback: register now (only works if the download was already triggered).
		downloadDir = filepath.Join(os.TempDir(), "brz-downloads")
		os.MkdirAll(downloadDir, 0755)
		wait = e.browser.WaitDownload(downloadDir)
	}

	// Honor d.Timeout. rod's WaitDownload returns a func that blocks
	// indefinitely; without bounding it here, a click that didn't
	// actually trigger a download would hang the executor forever
	// (no retry, no cancel, no recovery). Default 60s matches a sane
	// upper bound for most real download flows.
	timeout := 60 * time.Second
	if d.Timeout != "" {
		timeout = ParseTimeout(d.Timeout)
	}

	type waitOut struct{ info *proto.PageDownloadWillBegin }
	resCh := make(chan waitOut, 1)
	go func() { resCh <- waitOut{info: wait()} }()

	var info *proto.PageDownloadWillBegin
	select {
	case r := <-resCh:
		info = r.info
	case <-time.After(timeout):
		return fmt.Errorf("download timed out after %s (no file received)", timeout)
	}

	if info == nil {
		return fmt.Errorf("download failed: no download info received")
	}

	// The downloaded file is saved in downloadDir with the GUID as filename
	downloadPath := filepath.Join(downloadDir, info.GUID)
	e.LastDownload = downloadPath

	e.emit(events.Event{
		Event: events.DownloadStarted,
		Path:  downloadPath,
		URL:   info.URL,
	})

	// Honor save_as / save_to: rename the captured file to the requested
	// path. SaveAs takes precedence over SaveTo when both are set so older
	// workflows that use save_as keep working unchanged.
	saveTarget := d.SaveAs
	if saveTarget == "" {
		saveTarget = d.SaveTo
	}
	if saveTarget != "" {
		expanded := InterpolateEnv(saveTarget, e.workflow.Env)
		expanded, err := expandHomeDir(expanded)
		if err == nil && expanded != "" {
			if mkErr := os.MkdirAll(filepath.Dir(expanded), 0o755); mkErr == nil {
				if rerr := moveFile(downloadPath, expanded); rerr == nil {
					e.LastDownload = expanded
					downloadPath = expanded
				} else if e.debug {
					log.Printf("download save: rename %q → %q failed: %v", downloadPath, expanded, rerr)
				}
			}
		}
	}

	// Read file content into LastResult (preserve existing behavior)
	if data, err := os.ReadFile(downloadPath); err == nil {
		e.LastResult = string(data)
	}

	// Emit download_completed with final path + size.
	var downloadSize int64
	if fi, ferr := os.Stat(downloadPath); ferr == nil {
		downloadSize = fi.Size()
	}
	e.emit(events.Event{
		Event: events.DownloadCompleted,
		Path:  downloadPath,
		Size:  downloadSize,
	})

	// return_to: re-navigate the tab once the download is captured. A
	// click-triggered download often leaves the page at about:blank — without
	// this, subsequent wait_url / interaction steps fail.
	if d.ReturnTo != "" {
		var returnURL string
		if d.ReturnTo == "previous" {
			returnURL = preDownloadURL
		} else {
			returnURL = InterpolateEnv(d.ReturnTo, e.workflow.Env)
		}
		if returnURL != "" && returnURL != "about:blank" {
			if err := e.page.Navigate(returnURL); err == nil {
				_ = e.page.WaitLoad()
			} else if e.debug {
				log.Printf("download return_to %q failed: %v", returnURL, err)
			}
		}
	}

	return nil
}

// expandHomeDir resolves a leading "~" in a path. Returns the input unchanged
// if no expansion is needed. Used so save_as / save_to can take "~/Downloads/x.csv"
// without forcing every workflow author to expand home themselves.
func expandHomeDir(path string) (string, error) {
	if path == "" || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path, err
	}
	if path == "~" {
		return home, nil
	}
	if len(path) > 1 && path[1] == '/' {
		return filepath.Join(home, path[2:]), nil
	}
	// "~user/..." form is not supported; return as-is so callers see a clear
	// missing-file error downstream rather than a silently-wrong path.
	return path, nil
}

// moveFile renames src to dst, falling back to copy+remove when rename fails
// (typically because the source and destination are on different filesystems
// — e.g. /tmp on a tmpfs and ~/Downloads on the user's primary disk).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, rerr := os.ReadFile(src)
	if rerr != nil {
		return rerr
	}
	if werr := os.WriteFile(dst, data, 0o644); werr != nil {
		return werr
	}
	_ = os.Remove(src)
	return nil
}

func (e *Executor) doWaitVisible(w *WaitStep) error {
	timeout := ParseTimeout(w.Timeout)
	_, err := e.page.Timeout(timeout).Element(w.Selector)
	if err != nil && w.AliasName != "" {
		return fmt.Errorf("wait_visible %s: %w", formatSelectorForError(w.Selector, w.AliasName, e.workflow), err)
	}
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

// doWaitEnabled blocks until the target element is present in the DOM AND
// considered enabled: no `disabled` property and no `aria-disabled="true"`.
//
// Motivating case: forms protected by anti-bot challenges keep their submit
// button disabled until a verification token is received. Workflows that
// previously did fill → click submit silently no-op'd because the click
// landed on a disabled button. With wait_enabled between fill and click,
// brz blocks until the element actually becomes interactable.
func (e *Executor) doWaitEnabled(w *WaitStep) error {
	timeout := ParseTimeout(w.Timeout)
	deadline := time.Now().Add(timeout)
	pollInterval := 500 * time.Millisecond

	for {
		// Re-look-up the element each poll. Page.Has is non-blocking so
		// we don't accidentally wait forever when the selector never matches
		// (e.g. user typo) — that case lands in the deadline check below.
		has, hasEl, _ := e.page.Has(w.Selector)
		if has && hasEl != nil {
			res, evalErr := hasEl.Eval(`function() {
				if (this.disabled === true) return false;
				const aria = this.getAttribute('aria-disabled');
				if (aria === 'true') return false;
				return true;
			}`)
			if evalErr == nil && res.Value.Bool() {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("element %s did not become enabled within %s", formatSelectorForError(w.Selector, w.AliasName, e.workflow), timeout)
		}
		time.Sleep(pollInterval)
	}
}

// doHandoff pauses the workflow for a human. Ensures the browser is
// headed (relaunching if currently headless via the existing restart()
// machinery), prints the message + current URL to stderr, then polls
// for the resume signal until either it fires or the timeout elapses.
//
// The handoff timeout defaults to 10 minutes — long enough for a person
// to solve a captcha or click through a 2FA flow, short enough to not
// silently hang an automated pipeline forever. The 1s poll cadence is
// the same as wait_url; faster wouldn't help and would burn CPU.
//
// We assume the caller of RunAction holds e.mu (it does — see RunAction
// at the public entry). restart() and announceHeadedLaunch are both
// safe to invoke under that lock with the production os.Stderr writer.
func (e *Executor) doHandoff(h *HandoffStep) error {
	if h.WaitURL == "" && h.WaitEval == "" {
		return fmt.Errorf("handoff requires either wait_url or wait_eval (set neither — workflow would hang)")
	}
	if h.WaitURL != "" && h.WaitEval != "" {
		return fmt.Errorf("handoff: set wait_url OR wait_eval, not both")
	}

	timeout := 10 * time.Minute
	if h.Timeout != "" {
		timeout = ParseTimeout(h.Timeout)
	}

	// Ensure headed. If we're currently headless, capture the current
	// URL FIRST (so we can re-navigate to it after the relaunch),
	// then restart the browser. closeLocked + startLocked replace the
	// rod browser handle entirely, so the existing e.page becomes a
	// stale reference into a closed Chromium — we must rehydrate it via
	// setupPage with the captured URL, not reuse the handle.
	if !e.headed {
		var preRestartURL string
		if e.page != nil {
			if info, ierr := e.page.Info(); ierr == nil {
				preRestartURL = info.URL
			}
		}
		if err := e.restart(true); err != nil {
			return fmt.Errorf("handoff: failed to escalate to headed mode: %w", err)
		}
		// restart's startLocked stashed the announce; flush it now so
		// the user sees the same launch line they'd get from a normal
		// headed Start. We hold e.mu — production os.Stderr write is
		// non-blocking; tests inject only synchronous buffers.
		if e.pendingAnnounceFlag {
			pid, exe := e.pendingAnnouncePID, e.pendingAnnounceExe
			e.pendingAnnouncePID, e.pendingAnnounceExe, e.pendingAnnounceFlag = 0, "", false
			e.announceHeadedLaunch(pid, exe)
			e.surfaceMacOSWindow(exe)
		}
		// CRITICAL: e.page is now a stale handle pointing at the
		// closed-headless browser. The previous version of this code
		// called setupPage(Action{}) which "reused" that stale handle
		// and the subsequent Info()/Eval() polls in the resume loop
		// silently failed. Re-navigate to the URL we were on (or
		// about:blank as a safe fallback) via setupPage with an
		// explicit URL so the page handle is replaced.
		restoreURL := preRestartURL
		if restoreURL == "" || restoreURL == "about:blank" {
			restoreURL = "about:blank"
		}
		// Force-nil the stale page handle so setupPage recreates rather
		// than reuses. setupPage's "URL matches current page" fast-path
		// would otherwise see the stale URL on the dead handle.
		e.page = nil
		if errMsg := e.setupPage(Action{URL: restoreURL, ForceNavigate: true}); errMsg != "" {
			return fmt.Errorf("handoff: failed to set up page after relaunch: %s", errMsg)
		}
	}

	// Print the handoff banner. stderr because stdout is reserved for
	// JSON output / structured tool consumption.
	currentURL := ""
	if info, err := e.page.Info(); err == nil {
		currentURL = info.URL
	}
	msg := h.Message
	if msg == "" {
		msg = "Workflow paused for human takeover."
	}
	fmt.Fprintf(os.Stderr, "[brz] HANDOFF: %s\n", msg)
	if currentURL != "" {
		fmt.Fprintf(os.Stderr, "[brz] HANDOFF: current page = %s\n", currentURL)
	}
	if h.WaitURL != "" {
		fmt.Fprintf(os.Stderr, "[brz] HANDOFF: workflow will resume when URL contains %q (timeout %s)\n", h.WaitURL, timeout)
	} else {
		fmt.Fprintf(os.Stderr, "[brz] HANDOFF: workflow will resume when JS eval returns truthy (timeout %s)\n", timeout)
	}

	deadline := time.Now().Add(timeout)
	pollInterval := 1 * time.Second
	for {
		if h.WaitURL != "" {
			info, err := e.page.Info()
			if err == nil && strings.Contains(info.URL, h.WaitURL) {
				fmt.Fprintln(os.Stderr, "[brz] HANDOFF: resumed (URL matched)")
				return nil
			}
		} else {
			// WaitEval must be a JS function expression (e.g.
			// `() => document.cookie.includes('session=')`). We wrap it
			// in `Boolean((${expr})())` so JS-truthy values like 1,
			// "done", or DOM nodes resume the workflow — not just
			// strict-bool true. rod's res.Value.Bool() interprets
			// JSON-true reliably; the wrap guarantees the inner JS
			// always returns one of true/false.
			wrapped := "() => Boolean((" + h.WaitEval + ")())"
			res, err := e.page.Eval(wrapped)
			if err == nil && res.Value.Bool() {
				fmt.Fprintln(os.Stderr, "[brz] HANDOFF: resumed (eval matched)")
				return nil
			}
		}
		if time.Now().After(deadline) {
			if h.WaitURL != "" {
				return fmt.Errorf("handoff: URL did not match %q within %s", h.WaitURL, timeout)
			}
			return fmt.Errorf("handoff: eval did not return truthy within %s", timeout)
		}
		time.Sleep(pollInterval)
	}
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

// extractMacOSBundleName returns the .app stem from a Chromium executable
// path, or "" if the path doesn't sit inside an .app bundle.
//
//	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"           → "Google Chrome"
//	".../chromium-1208/.../Google Chrome for Testing.app/Contents/MacOS/..." → "Google Chrome for Testing"
//	"/usr/bin/chromium"                                                      → ""
//	""                                                                       → ""
//
// Pure string scan from the right, no syscalls — safe to call from any goroutine.
func extractMacOSBundleName(exePath string) string {
	if exePath == "" {
		return ""
	}
	parts := strings.Split(exePath, string(filepath.Separator))
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.HasSuffix(parts[i], ".app") {
			return strings.TrimSuffix(parts[i], ".app")
		}
	}
	return ""
}

// surfaceMacOSWindow asks macOS to bring the just-launched Chromium to the
// foreground via osascript. Non-Darwin platforms and exe paths without a
// .app bundle are silent no-ops. We bound osascript to a 2s timeout and
// swallow every error: this is purely a UX nicety, never a blocker.
//
// Why this matters: when brz launches headed Chromium on macOS while the
// user is doing anything else, the new window opens behind the foreground
// app. Without this hint, manual_login flows time out at wait_url with the
// user thinking nothing happened. Cheap activate solves it for the vast
// majority of cases; users who explicitly hide it (Cmd+H) still have to
// Cmd+Tab themselves, which the announcement line tells them to do.
func (e *Executor) surfaceMacOSWindow(exePath string) {
	if runtime.GOOS != "darwin" {
		return
	}
	bundle := extractMacOSBundleName(exePath)
	if bundle == "" {
		return
	}
	cmd := exec.Command("osascript", "-e",
		fmt.Sprintf(`tell application %q to activate`, bundle))
	// Best-effort timeout: kill osascript after 2s if it hangs (rare, but
	// possible if the user has an Accessibility prompt up).
	done := make(chan struct{})
	go func() {
		select {
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		case <-done:
		}
	}()
	_ = cmd.Run()
	close(done)
}

// announceHeadedLaunch writes a single stderr line whenever brz launches a
// visible Chromium. Without this line, headed mode is invisible to users on
// macOS — the window often opens behind whatever app currently has focus,
// and there's no signal that the browser is actually open. The line names
// the PID, exe path, and profile dir so a user (or LLM driving brz) can
// reliably surface the window or kill it.
//
// Callers must NOT hold e.mu while invoking this (production callers don't —
// see Start). The writer is os.Stderr by default; tests can override via
// setAnnounceWriterForTest. We do not expose a public override because a
// blocking user-supplied writer would stall every executor method, and the
// announce path is a UX nicety, not load-bearing logic.
func (e *Executor) announceHeadedLaunch(pid int, exePath string) {
	var w io.Writer = os.Stderr
	if e.announceWriter != nil {
		w = e.announceWriter
	}
	profile := e.profileDir
	if profile == "" {
		profile = "(default)"
	}
	exe := exePath
	if exe == "" {
		exe = "(unknown)"
	}
	if pid > 0 {
		fmt.Fprintf(w, "[brz] Headed Chromium launched (pid=%d exe=%s profile=%s) — Cmd+Tab to focus if not visible\n",
			pid, exe, profile)
	} else {
		fmt.Fprintf(w, "[brz] Headed Chromium launched (exe=%s profile=%s) — Cmd+Tab to focus if not visible\n",
			exe, profile)
	}
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
	e.emit(events.Event{
		Event: events.ScreenshotCaptured,
		Path:  path,
		Size:  int64(len(data)),
	})
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

// captureSimilarElements runs SimilarElementsJS on the current page to find
// elements similar to the failed step's target. Returns up to 5 elements.
// Returns nil on any error (defensive — don't mask the real failure).
func (e *Executor) captureSimilarElements(selector string) []ElementInfo {
	if e.page == nil {
		return nil
	}
	res, err := e.page.Timeout(2*time.Second).Eval(SimilarElementsJS, selector)
	if err != nil {
		return nil
	}
	var elements []ElementInfo
	raw := res.Value.JSON("", "")
	if err := json.Unmarshal([]byte(raw), &elements); err != nil {
		return nil
	}
	return elements
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

// Browser returns the underlying rod.Browser handle for advanced usage —
// e.g. the MCP server (which manages its own page lifecycle), or attaching
// a HijackRequests router for record/replay. Returns nil if Start() hasn't
// completed CDP connection. Callers must coordinate their own locking; the
// executor's mu is not held while the returned browser is in use.
func (e *Executor) Browser() *rod.Browser {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.browser
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

// BrowserVersionString returns the running browser's product string
// (e.g. "Chrome/131.0.6778.86"). Best-effort: empty on any failure.
// Used by the failure-bundle writer.
func (e *Executor) BrowserVersionString() string {
	e.mu.Lock()
	browser := e.browser
	e.mu.Unlock()
	if browser == nil {
		return ""
	}
	ver, err := (proto.BrowserGetVersion{}).Call(browser)
	if err != nil {
		return ""
	}
	return ver.Product
}

// CaptureForensics grabs a screenshot + DOM HTML for the failure bundle.
// Time-bounded so a hung page can't block exit. Each artifact is independent;
// a screenshot failure does not prevent DOM capture (and vice versa).
// Caller must invoke this BEFORE Close() — page handle goes away on close.
//
// NOTE: screenshot and DOM are captured sequentially; on highly dynamic
// pages they may describe slightly different render states. Acceptable for
// post-mortem forensics; not suitable as a synchronized snapshot.
func (e *Executor) CaptureForensics(timeout time.Duration) (screenshot []byte, dom string, ssErr, domErr error) {
	e.mu.Lock()
	page := e.page
	e.mu.Unlock()
	if page == nil {
		ssErr = fmt.Errorf("no page available")
		domErr = ssErr
		return
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// Screenshot: non-Must variant, time-bounded.
	func() {
		defer func() {
			if r := recover(); r != nil {
				ssErr = fmt.Errorf("screenshot panic: %v", r)
			}
		}()
		data, err := page.Timeout(timeout).Screenshot(true, nil)
		if err != nil {
			ssErr = err
			return
		}
		screenshot = data
	}()
	func() {
		defer func() {
			if r := recover(); r != nil {
				domErr = fmt.Errorf("dom panic: %v", r)
			}
		}()
		html, err := page.Timeout(timeout).HTML()
		if err != nil {
			domErr = err
			return
		}
		dom = html
	}()
	return
}

// ConsoleSink collects browser console messages. Safe for concurrent use.
type ConsoleSink struct {
	mu    sync.Mutex
	lines []string
	stop  func()
}

// Lines returns the captured console messages, joined with newlines.
func (c *ConsoleSink) Lines() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// Stop ends the listener. Safe to call multiple times.
func (c *ConsoleSink) Stop() {
	c.mu.Lock()
	stop := c.stop
	c.stop = nil
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// AttachConsoleSink starts a browser-level listener that records every
// Runtime.consoleAPICalled event into a sink. Returns nil if no browser
// is running. The listener stops when sink.Stop() is called or when the
// browser closes (whichever comes first).
func (e *Executor) AttachConsoleSink() *ConsoleSink {
	e.mu.Lock()
	browser := e.browser
	e.mu.Unlock()
	if browser == nil {
		return nil
	}
	sink := &ConsoleSink{}
	cancelCtx, cancel := browser.WithCancel()
	wait := cancelCtx.EachEvent(func(ev *proto.RuntimeConsoleAPICalled) {
		var parts []string
		for _, a := range ev.Args {
			parts = append(parts, fmt.Sprintf("%v", a.Value))
		}
		line := fmt.Sprintf("[%s] %s", ev.Type, strings.Join(parts, " "))
		sink.mu.Lock()
		// Cap at 5000 lines to bound memory.
		if len(sink.lines) < 5000 {
			sink.lines = append(sink.lines, line)
		}
		sink.mu.Unlock()
	})
	sink.stop = cancel
	go func() {
		// wait() blocks until cancel() or browser shuts down.
		wait()
	}()
	return sink
}
