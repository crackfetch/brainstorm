# Getting Started with Brainstorm

Brainstorm (`brz`) automates browser tasks. You can explore pages interactively, or define repeatable workflows in YAML and run them from the command line.

## Installation

### Homebrew (macOS/Linux)

```bash
brew install crackfetch/tap/brz
```

### Download a binary

Go to [Releases](https://github.com/crackfetch/brainstorm/releases) and download the binary for your platform:

| Platform | File |
|----------|------|
| Mac (Apple Silicon) | `brainstorm_*_darwin_arm64.tar.gz` |
| Mac (Intel) | `brainstorm_*_darwin_amd64.tar.gz` |
| Linux | `brainstorm_*_linux_amd64.tar.gz` |
| Windows | `brainstorm_*_windows_amd64.zip` |

```bash
# Mac/Linux
tar xzf brainstorm_*_darwin_arm64.tar.gz
chmod +x brz
./brz help
```

### Build from source

```bash
git clone https://github.com/crackfetch/brainstorm.git
cd brainstorm
go build -o brz ./cmd/brz
```

## Browser Requirements

brz drives a real browser via the Chrome DevTools Protocol.

**Lookup order:**
1. Your existing Chrome or Chromium installation (most people have this — zero download)
2. Previously cached Chromium from a prior run
3. Auto-download: if nothing is found, brz downloads a compatible Chromium (~200MB, one time)

## Exploring a Page

Before writing any workflow YAML, you can explore a page directly from the command line. These commands are the fastest way to understand a site's structure.

### Inspect: discover interactive elements

```bash
brz inspect https://example.com/login
```

Returns every input, button, link, select, and form field on the page, with their CSS selectors, types, names, and text. Hidden elements (CSRF tokens, hidden inputs) are included.

```json
{
  "ok": true,
  "url": "https://example.com/login",
  "title": "Login",
  "elements": [
    {"selector": "input#email", "tag": "input", "type": "email", "name": "email", "placeholder": "Email"},
    {"selector": "input#password", "tag": "input", "type": "password", "name": "password"},
    {"selector": "button.submit", "tag": "button", "text": "Sign In"}
  ]
}
```

Use `--full` to see all visible elements (capped at 500):

```bash
brz inspect https://example.com/login --full
```

### Screenshot: visual context

```bash
brz screenshot https://example.com/login
```

Returns the file path to a PNG screenshot:

```json
{"ok": true, "file": "/tmp/brz-screenshot-123456.png", "size": 234567}
```

### Eval: run JavaScript

```bash
brz eval https://example.com "document.title"
brz eval https://example.com "document.querySelectorAll('form').length"
brz eval https://example.com "document.querySelector('#price').textContent"
```

Returns the JavaScript result:

```json
{"ok": true, "result": "Example Domain"}
```

Promises are automatically awaited:

```bash
brz eval https://api.example.com "await fetch('/status').then(r => r.json())"
```

## Writing a Workflow

Once you understand a page's structure (from `brz inspect`), write a workflow YAML:

```yaml
name: example
actions:
  login:
    url: https://example.com/login
    steps:
      - fill: { selector: 'input#email', value: '${EMAIL}' }
      - fill: { selector: 'input#password', value: '${PASSWORD}' }
      - click: { selector: 'button.submit' }
      - wait_url: { match: 'dashboard', timeout: '30s' }

  export:
    url: https://example.com/export
    steps:
      - click: { selector: '#export-btn' }
      - download: { timeout: '60s' }
```

The selectors (`input#email`, `button.submit`) come directly from the `brz inspect` output.

## Running a Workflow

```bash
# Validate syntax first
brz validate my-workflow.yaml

# List available actions
brz actions my-workflow.yaml

# Run a specific action
brz run my-workflow.yaml login --env EMAIL=me@co.com --env PASSWORD=secret

# Run an export and get the file path
brz run my-workflow.yaml export | jq -r .download
```

### Headed mode (visible browser)

Useful for debugging or when a site requires CAPTCHA solving:

```bash
brz run my-workflow.yaml login --headed
brz inspect https://example.com --headed
```

### Auto-headed mode

Set `BRZ_HEADED=auto` to let brz start headless and automatically pop up a browser window when it needs help. Mark actions that might require user interaction with `headed: true` in your YAML:

```yaml
actions:
  login:
    headed: true   # auto mode will escalate to headed if headless fails
    url: https://example.com/login
    steps:
      - fill: { selector: '#email', value: '${EMAIL}' }
      - click: { selector: '#submit' }
  export:
    url: https://example.com/export
    steps:
      - click: { selector: '#export-btn' }
      - download: { timeout: '60s' }
```

When cookies are valid, `login` runs silently in headless mode. When cookies expire and the action fails, brz restarts the browser in headed mode and retries.

### Debug mode

Enables verbose logging and saves screenshots on failure:

```bash
brz run my-workflow.yaml login --debug
```

### JSON output

JSON is automatic when piped. Force it in a terminal with `--json`:

```bash
brz run my-workflow.yaml export --json
brz inspect https://example.com --json
```

## Optional Steps

Mark a step as `optional: true` to continue execution even if it fails. This is useful for elements that may not always be present, like a "Remember Me" checkbox or a dismissible banner.

```yaml
actions:
  login:
    url: https://example.com/login
    steps:
      - fill: { selector: 'input[name="Email"]', value: '${EMAIL}' }
      - fill: { selector: 'input[name="Password"]', value: '${PASSWORD}' }
      - label: "Check Remember Me"
        click: { selector: '#RememberMe', timeout: '3s' }
        optional: true
      - click: { selector: 'button', text: 'Sign In' }
```

If the "Remember Me" checkbox doesn't exist, the step is skipped with a log message and "Sign In" is still clicked. The action result remains `ok: true` as long as all non-optional steps succeed.

## Using Environment Variables

Keep credentials out of your workflow files:

```yaml
name: my-app
actions:
  login:
    url: https://app.example.com/login
    steps:
      - fill: { selector: '#email', value: '${MY_EMAIL}' }
      - fill: { selector: '#password', value: '${MY_PASSWORD}' }
      - click: { selector: '#submit' }
```

Pass them via `--env` flags:

```bash
brz run workflow.yaml login --env MY_EMAIL=user@example.com --env MY_PASSWORD=secret
```

Or set them in a `.env` file:

```bash
# .env
MY_EMAIL=user@example.com
MY_PASSWORD=secret
```

## Selecting Dropdowns

Use the `select` step to set dropdown values. It auto-detects native `<select>` elements and Select2 (jQuery) dropdowns:

```yaml
- select: { selector: '#category', value: '3' }
- select: { selector: '#dept', text: 'Engineering' }
```

## Viewport Configuration

Default viewport is 1280x900. Override at the workflow or action level:

```yaml
name: my-workflow
viewport:
  width: 1440
  height: 1080
actions:
  mobile_test:
    viewport: { width: 375, height: 812 }
    steps:
      - screenshot: "mobile.png"
```

## Eval Assertions

Verify that an action produced the right result with `eval:` blocks:

```yaml
actions:
  export:
    steps:
      - click: { selector: '#export-btn' }
      - download: { timeout: '60s' }
    eval:
      - label: "CSV has data"
        download_min_rows: 1
      - label: "Still on export page"
        url_contains: "export"
```

If any eval fails, the action result has `ok: false`. See the [Workflow Spec](workflow-spec.md) for all 9 eval types.

## Download and Upload

Export a file from one page, then upload it to another:

```yaml
name: transfer
actions:
  export:
    url: https://source.example.com/export
    steps:
      - click: { selector: '#export-csv' }
      - download: { timeout: '60s' }

  import:
    url: https://dest.example.com/import
    steps:
      - upload: { selector: 'input[type="file"]', source: 'result' }
      - click: { selector: '#submit' }
      - wait_text: { text: 'Upload complete', timeout: '30s' }
```

The `source: "result"` directive passes the file from the previous download.

## Persistent Browser Profile

brz saves browser data (cookies, sessions) to `~/.config/brz/chrome-profile/`. This means:

- Login sessions persist between runs and across all commands
- `brz run` login once, then `brz inspect` authenticated pages
- You don't need to re-authenticate every time
- First run may require `--headed` for initial login/CAPTCHA, or use `BRZ_HEADED=auto` to let brz decide
- If Chrome previously crashed, brz auto-recovers stale lock files
- Use `--ephemeral` for a clean session

## The Full LLM Agent Loop

If you're an LLM agent, here's the complete workflow for automating a new site:

```bash
# 1. Discover: what's on the page?
brz inspect https://seller.example.com/login

# 2. Generate: write workflow YAML using the selectors from step 1

# 3. Validate: check the YAML
brz validate seller-workflow.yaml

# 4. Execute: run it
brz run seller-workflow.yaml login --env EMAIL=$E --env PASSWORD=$P

# 5. Chain: run more actions (session persists)
file=$(brz run seller-workflow.yaml export | jq -r '.download')
```

## Next Steps

- Read the full [Workflow YAML Specification](workflow-spec.md)
- Browse [example workflows](../workflows/examples/)
- See the [Architecture Guide](architecture.md) for how brz works internally
