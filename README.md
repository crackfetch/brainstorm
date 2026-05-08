# Brainstorm

**[Website](https://crackfetch.github.io/brainstorm/)** · **[Docs](https://github.com/crackfetch/brainstorm/tree/main/docs)** · **[LLM Prompt](https://crackfetch.github.io/brainstorm/llms-full.txt)**

Browser automation for humans and machines. Inspect pages, write workflows in YAML, execute them with the `brz` CLI, get structured JSON results.

Brainstorm drives a real Chrome browser — discover page elements, fill forms, click buttons, download and upload files. The binary contains no site-specific code. What you automate is defined entirely by the workflow YAML you provide.

Designed for LLM agents: JSON output by default when piped, semantic exit codes, persistent browser sessions, page discovery commands, and rich `--help` documentation.

## Quick Start

```bash
# Install (macOS)
brew install crackfetch/tap/brainstorm

# Or build from source
go build -o brz ./cmd/brz

# Discover what's on a page
brz inspect https://example.com/login

# Take a screenshot
brz screenshot https://example.com

# Run JavaScript on a page
brz eval https://example.com "document.title"

# Write a workflow, validate it, run it
brz validate my-workflow.yaml
brz run my-workflow.yaml login --headed --env EMAIL=me@co.com
```

On first run, brz uses your existing Chrome installation. If Chrome isn't found, it downloads a compatible Chromium automatically.

## Commands

### Page Discovery (no workflow needed)

```
brz inspect <url> [--full]              Discover interactive elements on a page
brz screenshot <url> [--output FILE]    Capture a full-page screenshot
brz eval <url> <js-expression>          Execute JavaScript and return the result
brz probe [--headless] <url>            Interactive selector REPL — type CSS/XPath, see live matches and highlights
```

### Workflow Execution

```
brz run <workflow.yaml> <action> [flags]    Execute a workflow action
  --events=jsonl                              Emit structured JSONL lifecycle events on stdout (orchestrator-friendly)
  --bundle-on-fail=auto|never                 Write a forensics tarball on failure (default: auto, ~/.brz/failures/)
  --baseline <path|auto>                      On success, write a drift baseline (selector hits per step)
  --check-drift <baseline>                    Compare this run against a baseline; warn on selector drift
  --strict-drift                              Make any drift event a terminal failure (exit code 4)
brz validate [--strict] <workflow.yaml>     Check YAML syntax. --strict rejects unknown fields with "Did you mean?" suggestions.
brz fmt [--diff|--stdout] <file>...         Canonicalize workflow YAML (gofmt-style). Stable key order, comments preserved, idempotent. `--diff` for CI.
brz lint [--strict] [--json] <file>...      Schema-check + smell-check. Superset of `validate --strict`. Flags brittle selectors, untimed downloads, JS-truthy eval, duplicate step labels, undeclared env vars.
brz record <wf.yaml> <action> [--cassette FILE]   Run + capture every (request, response) pair into a JSON cassette
brz replay <wf.yaml> <action> --from <cassette>   Re-run against the cassette with zero network (CI / regression check)
brz actions <workflow.yaml>                 List available actions
```

### Diagnostics

```
brz status [--json]                        Snapshot of brz state: profile dir, SingletonLock files, running Chromiums, downloads tmpdir, failure-artifact counts.
brz logs [--follow] [--limit N] [--json]   List recent failure-screenshot artifacts newest-first. --follow tails new ones as they appear (NDJSON).
brz examples [list|cat|scaffold]           Bundled workflow YAML patterns. Run `brz examples list` to see what's available; `cat <name>` prints one; `scaffold <name>` writes it into the cwd.
```

### Utility

```
brz mcp        Run a Model Context Protocol server over stdio
brz version    Print version
brz help       Show full help with output schemas
```

Run `brz <command> --help` for detailed usage, JSON schemas, and examples.

---

## MCP Server Mode

`brz mcp` turns brainstorm into a Model Context Protocol server. Any LLM client that speaks MCP — Claude Code, Cursor, the MCP inspector — can introspect the available tools and drive a real Chrome browser through tool calls. No YAML required.

```bash
brz mcp                              # default: 5-minute idle timeout, headless
brz mcp --headed --idle-timeout 30m  # visible browser, longer idle window
brz mcp --profile ~/my-chrome-data   # custom Chrome profile dir
```

The server speaks JSON-RPC 2.0 (newline-delimited) over stdin/stdout. All log output goes to stderr so the JSON-RPC stream stays clean.

### Tools exposed (v1)

| Tool                   | Purpose                                                     |
| ---------------------- | ----------------------------------------------------------- |
| `browser_goto`         | Navigate to a URL.                                          |
| `browser_click`        | Click the nth element matching a CSS selector.              |
| `browser_type`         | Focus a selector and type text. Optional submit/clear.      |
| `browser_extract`      | Read text/html/attribute from elements matching a selector. |
| `browser_screenshot`   | Capture a PNG (returned as MCP image content).              |
| `browser_eval`         | Run a JS expression and return its JSON value.              |
| `browser_wait_for`     | Wait for a selector to appear or a JS expression to be truthy. |
| `browser_get_url`      | Current URL + title.                                        |
| `browser_session_info` | Cookies count, viewport, user agent.                        |

Each tool's full input schema is returned by the standard `tools/list` MCP request, so any client can introspect without docs.

### Lifecycle

- Chrome is lazy-launched on the first tool call that needs a page.
- Concurrent tool calls are serialized through a single mutex around the browser handle.
- On stdin EOF (host disconnect) or `--idle-timeout` of inactivity, the browser is torn down cleanly. SIGINT/SIGTERM also tear down before exit.

### Wiring into Claude Code

Claude Code reads MCP servers from `~/.claude/mcp_settings.json` (or your project-local equivalent). Add an entry like:

```json
{
  "mcpServers": {
    "brz": {
      "command": "brz",
      "args": ["mcp"]
    }
  }
}
```

For Claude Desktop, the equivalent block lives in `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "brz": {
      "command": "/usr/local/bin/brz",
      "args": ["mcp", "--headed"]
    }
  }
}
```

Restart the host. The `brz` server will appear in the tool palette; the LLM can then drive the browser end-to-end.

### Known v1 limitations

- One browser per process. Multi-tab is not exposed; future tools could add it.
- Tool calls are serialized through a single mutex around the rod browser. Concurrent `tools/call` requests from a single client run one-after-the-other.
- A SIGINT received mid-tool-call is honored after the call returns (rod operations are not currently wired to a cancellable context).
- JSON-RPC batch requests are explicitly rejected (`-32600 Invalid Request`).

### Hand-driving the protocol

For debugging you can pipe JSON-RPC requests in directly. The example below sends a minimal-shape `initialize` (real MCP clients also include `protocolVersion`, `capabilities`, and `clientInfo` in `params` — `brz mcp` accepts the minimal form so probes stay short):

```bash
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | brz mcp --idle-timeout 5s
```

---

## Resilience Features

Long-running scrapers fail eventually. brz ships three primitives to make those failures cheap to diagnose and to make successful runs reproducible.

### Failure-artifact bundles

When `brz run` fails, brz writes a self-contained tarball to `~/.brz/failures/<timestamp>-<workflow>-<hash>.tar.gz`:

- `failure.json` — error + chain, brz/Chrome/OS versions
- `workflow.yaml` — verbatim copy of what ran
- `screenshot.png` + `dom.html` — page state at the moment of failure
- `console.log` — browser console captured during the run
- `events.jsonl` — minimal action_end record
- `stderr.log` — fd-level tee of this run's stderr
- `env.txt` — allowlisted env only (`BRZ_*`, `CHROME_*`, `DISPLAY`, etc. — never full env)

Bundle write errors never mask the original exit code. Best-effort per artifact: a missing screenshot doesn't abort the bundle. Defaults to `--bundle-on-fail=auto`; opt out with `never` or env `BRZ_BUNDLE_ON_FAIL=never`. Override the directory with `--bundle-dir` or `BRZ_BUNDLE_DIR`.

### Site-drift detection

Catch the silent failure mode where a selector "still works" but matches the wrong number of elements:

```bash
# Capture a baseline on a known-good run
brz run wf.yaml fetch --baseline auto

# Later runs check for drift
brz run wf.yaml fetch --check-drift wf.baseline.json
# warns to stderr: DRIFT [step=order-rows] selector .row matched 12 (was 14)

# CI mode: any drift fails the run with exit 4
brz run wf.yaml fetch --check-drift wf.baseline.json --strict-drift
```

Three signals: `selector_count_changed`, `selector_no_longer_matches`, `text_pattern_changed` (sha256 of trimmed first-match `innerText`). Default `brz run` is unchanged when these flags are absent — per-step probe overhead applies only when an observer is attached.

### Record + replay

Turn a one-time successful run into a reproducible test fixture. `brz record` captures every (request, response) pair via CDP `Fetch.enable`; `brz replay` serves them back from disk with no real network.

```bash
# Record a real run into a cassette
brz record wf.yaml fetch --cassette wf.cassette.json

# Replay later — zero network, fully deterministic
brz replay wf.yaml fetch --from wf.cassette.json --strict
```

Match key is `(method, canonical-URL, sha256(body))` — host lowercased, query keys sorted. `--strict` fails on any unmatched request (CI use). Default mode passes through to network with a stderr warning. Bodies stored base64 with a 5MB cap (override with `--no-body-cap`). WebSocket frames, iframe network state, and `data:` URLs are out of scope for v1.

Two big wins:
- **CI runs scrapers without hitting real sites** — faster, no flakes, no rate limits.
- **"Did the site change or did our workflow regress?"** Replay against yesterday's cassette: pass = site changed, fail = our regression.

### Structured event stream

For external orchestrators that need to react to a workflow as it runs:

```bash
brz run wf.yaml fetch --events=jsonl
# emits one JSON object per line on stdout:
# {"ts":"...","seq":1,"event":"workflow_start","workflow":"fetch"}
# {"ts":"...","seq":2,"event":"step_start","step":"goto","action":"goto"}
# {"ts":"...","seq":3,"event":"step_end","step":"goto","status":"ok","duration_ms":1240}
# ...
# {"ts":"...","seq":N,"event":"workflow_end","status":"ok","steps_total":12,"duration_ms":18402}
```

Stdout is pure JSONL in this mode; human output moves to stderr. Pipe through `jq` for live filtering, or stream-parse from your driver process.

---

## Page Discovery Commands

These commands let you explore a page before writing a workflow. They are the key to LLM-driven automation: an agent can inspect a page, understand its structure, and generate workflow YAML without human help.

### `brz inspect`

List interactive elements on a page. Returns CSS selectors, element types, names, placeholder text, and more. Hidden elements (CSRF tokens, hidden inputs) are included because they reveal form structure that isn't visible in a screenshot.

```bash
brz inspect https://example.com/login
```

**Default mode** returns only actionable elements: inputs, textareas, selects, buttons, links, file inputs, `[role="button"]`, and `contenteditable` elements.

**`--full` mode** returns all visible elements, capped at 500.

**JSON output:**
```json
{
  "ok": true,
  "url": "https://example.com/login",
  "title": "Login — Example App",
  "total": 6,
  "elements": [
    {"selector": "input#email", "tag": "input", "type": "email", "name": "email", "placeholder": "you@company.com"},
    {"selector": "input#password", "tag": "input", "type": "password", "name": "password"},
    {"selector": "input[type=\"hidden\"]", "tag": "input", "type": "hidden", "name": "csrf_token", "value": "abc123", "hidden": true},
    {"selector": "button.submit", "tag": "button", "text": "Sign In"},
    {"selector": "a.forgot", "tag": "a", "text": "Forgot password?", "href": "/reset"},
    {"selector": "a.signup", "tag": "a", "text": "Create account", "href": "/signup"}
  ],
  "duration_ms": 1200
}
```

**Element fields:**

| Field | Description |
|-------|-------------|
| `selector` | CSS selector, usable directly in workflow YAML `click`/`fill` steps |
| `tag` | HTML tag name (`input`, `button`, `a`, `select`, etc.) |
| `type` | Input type (`text`, `password`, `email`, `hidden`, `file`, `submit`) |
| `name` | Form field `name` attribute |
| `placeholder` | Placeholder text |
| `text` | Visible text content (buttons, links — truncated to 80 chars) |
| `href` | Link URL |
| `value` | Current value (only for hidden inputs — reveals CSRF tokens, IDs) |
| `role` | ARIA role attribute |
| `hidden` | `true` if element is not visible (display:none, visibility:hidden, zero-size) |

### `brz screenshot`

Capture a full-page screenshot. Returns the file path and size.

```bash
brz screenshot https://example.com
brz screenshot https://example.com --output page.png
```

**JSON output:**
```json
{
  "ok": true,
  "url": "https://example.com",
  "file": "/tmp/brz-screenshot-123456.png",
  "size": 234567,
  "duration_ms": 1200
}
```

### `brz eval`

Execute a JavaScript expression on a page and return the result. Promises are automatically awaited.

```bash
brz eval https://example.com "document.title"
brz eval https://example.com "document.querySelectorAll('a').length"
brz eval https://app.com/api "await fetch('/api/status').then(r => r.json())"
```

**JSON output:**
```json
{
  "ok": true,
  "url": "https://example.com",
  "result": "Example Domain",
  "duration_ms": 800
}
```

The result can be any JSON-serializable value: strings, numbers, booleans, objects, arrays.

---

## Workflow Execution

### Writing a Workflow

A workflow YAML file defines named **actions**, each with a URL and a sequence of **steps**:

```yaml
name: my-workflow
env:
  BASE_URL: https://example.com
actions:
  login:
    url: ${BASE_URL}/login
    steps:
      - fill: { selector: 'input[name="email"]', value: '${EMAIL}' }
      - fill: { selector: 'input[name="password"]', value: '${PASSWORD}' }
      - click: { selector: 'button[type="submit"]' }
      - wait_url: { match: 'dashboard', timeout: '30s' }

  export_data:
    url: ${BASE_URL}/export
    steps:
      - click: { selector: '#export-btn' }
      - download: { timeout: '60s' }

  import_data:
    url: ${BASE_URL}/import
    steps:
      - upload: { selector: 'input[type="file"]', source: 'result' }
      - click: { selector: '#submit' }
      - wait_text: { text: 'Upload complete', timeout: '30s' }
```

### Running a Workflow

```bash
# Validate syntax first
brz validate my-workflow.yaml

# List available actions
brz actions my-workflow.yaml

# Run an action
brz run my-workflow.yaml login --env EMAIL=me@co.com --env PASSWORD=secret

# Run and get the downloaded file path
brz run my-workflow.yaml export | jq -r .download

# Chain actions
brz run my-workflow.yaml login --env EMAIL=$E --env PASSWORD=$P && \
brz run my-workflow.yaml export | jq -r .download
```

### `brz run` JSON Output

**Success:**
```json
{
  "ok": true,
  "action": "export",
  "steps": 3,
  "duration_ms": 2100,
  "download": "/tmp/brz-downloads/abc123",
  "download_size": 51200
}
```

**Failure:**
```json
{
  "ok": false,
  "action": "login",
  "steps": 1,
  "duration_ms": 5030,
  "error": "find element \"#submit\": timeout",
  "failed_step": 2,
  "step_type": "click",
  "screenshot": "/tmp/login_failed_20260328-143022_1.png",
  "page_url": "https://example.com/login",
  "page_html": "<html>...</html>"
}
```

On failure, `page_url` and `page_html` capture the page state at the moment the step failed, so agents can debug without re-running. Failed selector steps also include `page_elements`: up to 5 scoped recovery candidates from the current page. For click failures targeting `input`, `button`, or `a`, Brainstorm captures compatible action controls across all three tags because sites often swap submit inputs, buttons, and styled links. Use `text` for buttons/links and `value` for submit/button/reset inputs when deciding whether a candidate matches the step objective.

If the action was auto-escalated from headless to headed (via `BRZ_HEADED=auto`), the result includes `"escalated": true`.

### Available Steps

| Step | Description | Key options |
|------|-------------|-------------|
| `navigate` | Go to a URL | string URL |
| `click` | Click an element | `selector`, `text`, `nth`, `timeout` |
| `fill` | Type into an input | `selector`, `value`, `clear` |
| `upload` | Set file on file input | `selector`, `source` (path or `"result"`) |
| `download` | Wait for file download | `timeout` |
| `wait_visible` | Wait for element to appear | `selector`, `timeout` |
| `wait_text` | Wait for text on page | `text`, `timeout` |
| `wait_url` | Wait for URL to match | `match`, `timeout` |
| `screenshot` | Save screenshot | string filename |
| `sleep` | Pause execution | `duration` (e.g. `"5s"`) |
| `eval` | Run JavaScript | string JS code |

Each step can include a `label` field for logging:
```yaml
- label: "Click the export button"
  click: { selector: '#export' }
```

### Environment Variable Interpolation

Use `${VAR_NAME}` in any string value. Resolution order:
1. Workflow-level `env:` map in the YAML file
2. `--env KEY=VAL` flags on the command line
3. OS environment variables (from .env files or shell)

If a variable is not found, the `${VAR_NAME}` placeholder is left as-is (no crash).

### Selector Aliases

Centralize brittle selectors with a top-level `aliases:` map. Reference them from any step as `${aliases.NAME}`:

```yaml
name: example
aliases:
  cart_button: '#header .cart'
  product_card: '.product-grid > .card'
steps:
  - click: { selector: '${aliases.cart_button}' }
  - wait_visible: { selector: '${aliases.product_card}' }
```

When the site changes one selector that's used in eight places, you fix it in one alias instead of eight step lines. For shared libraries across workflows, use `aliases_from:` to load alias maps from external YAML files (`~`, absolute, or workflow-relative paths supported; later files override earlier; inline `aliases:` always wins). See `workflows/examples/csv-export-and-post.yaml` for a converted example.

Undefined references error at parse time with a "did you mean" suggestion. Cycles in alias-of-alias chains are detected and rejected. Runtime errors include the alias name so you can trace which alias produced the bad selector.

### Click + Download Sequencing

When a `click` step is immediately followed by a `download` step, brz automatically registers the download listener before the click executes. This is required by the Chrome DevTools Protocol. Always structure your workflow as:

```yaml
- click: { selector: '#download-btn' }
- download: { timeout: '60s' }
```

Do not put other steps between the click and the download.

---

## Data Pipeline Patterns

These are the core patterns for moving data between systems. See `workflows/examples/` for complete YAML files.

### Export a CSV

```yaml
export_inventory:
  url: https://seller.example.com/inventory/export
  steps:
    - wait_visible: { selector: '#export-csv', timeout: '10s' }
    - click: { selector: '#export-csv' }
    - download: { timeout: '120s' }
```

```bash
# Run it, get the file path
file=$(brz run workflow.yaml export_inventory | jq -r '.download')
cat "$file"  # the CSV content
```

### Export with date range filters

Pass dates dynamically via `--env`:

```yaml
export_orders:
  url: https://seller.example.com/orders
  steps:
    - fill: { selector: '#date-from', value: '${DATE_FROM}', clear: true }
    - fill: { selector: '#date-to', value: '${DATE_TO}', clear: true }
    - click: { selector: '#apply-filter' }
    - wait_visible: { selector: '.results-table', timeout: '30s' }
    - click: { selector: '#export-csv' }
    - download: { timeout: '120s' }
```

```bash
brz run workflow.yaml export_orders --env DATE_FROM=01/01/2024 --env DATE_TO=03/31/2024
```

### POST data to an API with `eval`

Use `eval` with `fetch()` to call APIs directly from the browser context:

```yaml
post_to_api:
  url: https://seller.example.com
  steps:
    - label: "POST data to API"
      eval: |
        const resp = await fetch('${API_URL}/api/v1/sync', {
          method: 'POST',
          headers: {
            'Authorization': 'Bearer ${API_KEY}',
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({ name: '${NAME}', price: parseFloat('${PRICE}') })
        });
        if (!resp.ok) throw new Error('API error: ' + resp.status);
        return await resp.json();
```

```bash
brz run workflow.yaml post_to_api \
  --env API_URL=https://api.myapp.com \
  --env API_KEY=sk_123 \
  --env NAME="Widget" \
  --env PRICE=9.99
```

### POST a CSV to an API

Download a CSV from the browser, then POST it to your backend:

```yaml
upload_csv_to_api:
  steps:
    - label: "Read the last downloaded CSV and POST it"
      eval: |
        const form = new FormData();
        const blob = new Blob([document.brz_last_result], { type: 'text/csv' });
        form.append('file', blob, 'inventory.csv');
        const resp = await fetch('${API_URL}/api/v1/import', {
          method: 'POST',
          headers: { 'Authorization': 'Bearer ${API_KEY}' },
          body: form
        });
        if (!resp.ok) throw new Error('Upload failed: ' + resp.status);
        return await resp.json();
```

### Download → transform → re-upload

Export a CSV, upload the modified version back:

```yaml
import_prices:
  url: https://seller.example.com/pricing/import
  steps:
    - click: { selector: '#import-btn' }
    - wait_visible: { selector: '.import-dialog', timeout: '10s' }
    - upload: { selector: 'input[type="file"]', source: 'result' }
    - click: { selector: '#validate-import' }
    - wait_text: { text: 'Validation complete', timeout: '60s' }
    - click: { selector: '#confirm-import' }
    - wait_text: { text: 'Import complete', timeout: '120s' }
```

```bash
# Chain: export prices, then import updated prices
brz run workflow.yaml export_prices && brz run workflow.yaml import_prices
```

### One-shot eval for quick API calls

No workflow needed — use `brz eval` directly:

```bash
# GET an API endpoint
brz eval https://api.example.com "await fetch('/api/status').then(r => r.json())"

# POST JSON
brz eval https://api.example.com "await fetch('/api/records', {
  method: 'POST',
  headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer sk_123'},
  body: JSON.stringify({name: 'test', value: 42})
}).then(r => r.json())"

# Check response status
brz eval https://api.example.com "await fetch('/api/health').then(r => ({status: r.status, ok: r.ok}))"
```

---

## Output Behavior

All commands follow the same output convention:

| Context | Format |
|---------|--------|
| Interactive terminal (TTY) | Human-readable one-line summaries |
| Piped (`brz ... \| jq`) | Single-line JSON |
| `--json` flag | Single-line JSON (forced) |

No spinners, no color codes, no progress bars, no decoration in machine mode.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Action/step failed (element not found, timeout, JS error) |
| 2 | Workflow error (bad YAML, missing action, bad file path) |
| 3 | Browser error (Chrome not found, failed to launch, connection refused) |

## Global Flags

Available on all browser commands (`run`, `inspect`, `screenshot`, `eval`):

| Flag | Description |
|------|-------------|
| `--json` | Force JSON output |
| `--headed` | Show the browser window (useful for CAPTCHAs, debugging) |
| `--debug` | Verbose logging + screenshots on failure |
| `--profile DIR` | Chrome profile directory for session persistence |
| `--ephemeral` | Use a temporary profile (no cookies, no session reuse) |

## Configuration

brz reads environment variables from two locations (earlier values take precedence):
1. `~/.config/brz/agent.env` — stable per-user config
2. `.env` in the working directory — local overrides

| Variable | Description |
|----------|-------------|
| `BRZ_HEADED=1` | Always show browser window (equivalent to `--headed`) |
| `BRZ_HEADED=auto` | Start headless, escalate to headed when an action marked `headed: true` fails |
| `BRZ_DEBUG=1` | Verbose logging + failure screenshots (equivalent to `--debug`) |
| `BRZ_PROFILE_DIR` | Chrome profile path (default: `~/.config/brz/chrome-profile`) |

## Session Persistence

By default, brz reuses a Chrome profile at `~/.config/brz/chrome-profile/`. Login cookies and sessions survive between invocations. This means:

- Log in once with `--headed`, then run headless forever
- Use `BRZ_HEADED=auto` to let brz start headless and pop up a window only when it needs help (e.g., expired cookies on a `headed: true` action)
- Chain multiple `brz run` calls without re-authenticating
- `brz inspect` a page that requires login (cookies carry over from a prior `brz run`)
- Use `--ephemeral` for a clean session with no stored cookies

---

## For LLM Agents

brz is designed to be called as a subprocess by LLM agents (Claude Code, Cursor, Aider, custom agents). The complete workflow for an LLM automating a new site:

### Step 1: Discover the page

```bash
# See what's on the page (interactive elements, hidden fields, selectors)
brz inspect https://seller.example.com/login

# Or take a screenshot for visual context
brz screenshot https://seller.example.com/login | jq -r .file

# Probe something specific with JS
brz eval https://seller.example.com/login "document.forms[0].action"
```

### Step 2: Generate a workflow YAML

The LLM uses the selectors from `brz inspect` to write workflow YAML:

```yaml
name: seller-portal
actions:
  login:
    url: https://seller.example.com/login
    steps:
      - fill: { selector: 'input#email', value: '${EMAIL}' }
      - fill: { selector: 'input#password', value: '${PASSWORD}' }
      - click: { selector: 'button.submit' }
      - wait_url: { match: 'dashboard', timeout: '30s' }
```

### Step 3: Validate and run

```bash
brz validate seller-portal.yaml
brz run seller-portal.yaml login --env EMAIL=me@co.com --env PASSWORD=secret
```

### Key patterns

```bash
# Check exit code for success/failure
brz run site.yaml login && echo "logged in" || echo "failed"

# Get a downloaded file path
file=$(brz run site.yaml export | jq -r '.download')

# Conditional retry
result=$(brz run site.yaml export)
if echo "$result" | jq -e '.ok' > /dev/null; then
  file=$(echo "$result" | jq -r '.download')
fi

# Discover available actions
brz actions site.yaml | jq -r '.actions[].name'
```

---

## Go Library

Brainstorm's workflow engine is importable as a Go package:

```go
import "github.com/crackfetch/brainstorm/workflow"

// Load and run a workflow
w, _ := workflow.Load("my-workflow.yaml")
exec := workflow.NewExecutor(w, workflow.WithHeaded(true))
exec.Start()
defer exec.Close()
result := exec.RunAction("export")
fmt.Println(result.OK, result.Download)

// One-shot page inspection (no workflow needed)
exec := workflow.NewExecutor(nil, workflow.WithProfileDir("/tmp/profile"))
exec.Start()
defer exec.Close()
exec.NavigateTo("https://example.com")
// Use exec.Page() for direct rod access
```

### Public API

```go
// Executor lifecycle
workflow.NewExecutor(w *Workflow, opts ...Option) *Executor
exec.Start() error
exec.Close()
exec.NavigateTo(url string) error

// Workflow execution
exec.RunAction(name string) *ActionResult

// Configuration
workflow.WithHeaded(bool) Option
workflow.WithAutoHeaded(bool) Option    // start headless, escalate on failure
workflow.WithDebug(bool) Option
workflow.WithProfileDir(string) Option

// Environment
exec.SetEnv(key, value string)

// Browser access
exec.Page() *rod.Page
exec.IsHeaded() bool
exec.KeyPress(key input.Key) error
exec.WaitOnFailure()            // keep browser open in headed mode for debugging

// Result types
workflow.ActionResult   // ok, action, steps, duration_ms, download, error, page_html, page_url, escalated, ...
workflow.InspectResult  // ok, url, title, elements, total, truncated, ...
workflow.ElementInfo    // selector, tag, type, name, text, href, hidden, ...
```

## Building from Source

```bash
go build -o brz ./cmd/brz
```

### Cross-compile

```bash
GOOS=darwin  GOARCH=arm64 go build -o brz-darwin-arm64  ./cmd/brz
GOOS=linux   GOARCH=amd64 go build -o brz-linux-amd64   ./cmd/brz
GOOS=windows GOARCH=amd64 go build -o brz.exe           ./cmd/brz
```

## Responsible Use

Brainstorm is a general-purpose browser automation tool. It interacts with websites using your own credentials, on your own machine, on your behalf.

- **You are responsible** for complying with the terms of service of any website you automate.
- Workflow files define which sites and actions are automated. Review any workflow file before running it.
- Brainstorm does not access, scrape, or store data from accounts other than your own.
- This tool is intended for automating your own repetitive tasks — not for circumventing access controls, scraping third-party data, or any use that violates applicable laws or platform terms.

## License

MIT — see [LICENSE](LICENSE).
