# Brainstorm

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
```

### Workflow Execution

```
brz run <workflow.yaml> <action> [flags]   Execute a workflow action
brz validate <workflow.yaml>               Check YAML syntax
brz actions <workflow.yaml>                List available actions
```

### Utility

```
brz version    Print version
brz help       Show full help with output schemas
```

Run `brz <command> --help` for detailed usage, JSON schemas, and examples.

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
  "screenshot": "/tmp/login_failed_20260328-143022_1.png"
}
```

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
| `BRZ_HEADED=1` | Show browser window (equivalent to `--headed`) |
| `BRZ_DEBUG=1` | Verbose logging + failure screenshots (equivalent to `--debug`) |
| `BRZ_PROFILE_DIR` | Chrome profile path (default: `~/.config/brz/chrome-profile`) |

## Session Persistence

By default, brz reuses a Chrome profile at `~/.config/brz/chrome-profile/`. Login cookies and sessions survive between invocations. This means:

- Log in once with `--headed`, then run headless forever
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
workflow.WithDebug(bool) Option
workflow.WithProfileDir(string) Option

// Environment
exec.SetEnv(key, value string)

// Browser access
exec.Page() *rod.Page
exec.IsHeaded() bool
exec.KeyPress(key input.Key) error

// Result types
workflow.ActionResult   // ok, action, steps, duration_ms, download, error, ...
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
