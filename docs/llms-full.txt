# brz — Browser Automation CLI

> Paste this into your LLM's system prompt (Claude, GPT, Gemini, etc.) to teach it how to use brz.
> Works as-is with any LLM that accepts markdown instructions.

You have access to `brz`, a browser automation CLI. It drives a real Chrome browser via the DevTools Protocol. Use it to inspect pages, take screenshots, run JavaScript, and execute multi-step workflows defined in YAML. brz includes built-in stealth (anti-bot detection evasion) — no extra configuration needed for sites behind Cloudflare, PerimeterX, or DataDome.

## Quick Start

```bash
# 1. Discover what's on a page
brz inspect https://example.com/login

# 2. Take a screenshot for visual context
brz screenshot https://example.com/login

# 3. Run JavaScript on a page
brz eval https://example.com "document.title"

# 4. Write a YAML workflow using selectors from inspect output, then validate it
brz validate my-workflow.yaml

# 5. Execute a workflow action
brz run my-workflow.yaml login --env EMAIL=user@co.com --env PASSWORD=secret
```

Always start with `brz inspect` to discover selectors before writing workflows.

## Discovery Commands

### inspect — discover interactive elements

```bash
brz inspect <url> [--full] [--tag input,button] [--name email] [--compact] [--screenshot] [--eval "expr"] [--headed] [--json]
```

Returns every input, button, link, select, and form on the page with CSS selectors you can use directly in workflow YAML.

**Filters:** `--tag input,button` keeps only elements with matching tags. `--name email,password` keeps only elements with matching name attributes. Both compose (element must match both if both set).

**Compact mode:** `--compact` strips placeholder, href, value, role, and hidden fields from each element. Keeps only selector, tag, type, name, and text. ~40% fewer tokens.

**Combo mode:** `--screenshot` captures a screenshot in the same browser session (saves a cold-start). `--eval "document.title"` runs a JS expression. Both add fields to the JSON output.

```json
{
  "ok": true,
  "url": "https://example.com/login",
  "title": "Login",
  "total": 4,
  "elements": [
    {"selector": "input#email", "tag": "input", "type": "email", "name": "email"},
    {"selector": "input#password", "tag": "input", "type": "password", "name": "password"},
    {"selector": "button.submit", "tag": "button", "text": "Sign In"},
    {"selector": "input[type=\"hidden\"]", "tag": "input", "type": "hidden", "name": "csrf_token", "value": "abc123", "hidden": true}
  ],
  "duration_ms": 1200,
  "screenshot": "/tmp/brz-inspect-screenshot-123.png",
  "eval_result": "Login"
}
```

Use `--full` to return all visible elements (default: actionable only). `screenshot` and `eval_result` fields only appear when `--screenshot` / `--eval` flags are used.

### screenshot — capture a page

```bash
brz screenshot <url> [--output FILE] [--headed] [--json]
```

```json
{"ok": true, "url": "https://example.com", "file": "/tmp/brz-screenshot-123.png", "size": 234567, "duration_ms": 1200}
```

### eval — run JavaScript

```bash
brz eval <url> <js-expression> [--headed] [--json]
```

The expression is wrapped in a function and its return value is captured. Promises are automatically awaited.

```json
{"ok": true, "url": "https://example.com", "result": "Example Domain", "duration_ms": 800}
```

## Workflow Commands

### run — execute a workflow action

```bash
brz run <workflow.yaml> <action>[,action2,action3] [--env KEY=VAL]... [--headed] [--debug] [--dry-run] [--json]
```

**Multi-action:** Comma-separated action names run sequentially in one browser session (saves N-1 cold starts). Fail-fast on first failure. Output is always the last ActionResult.

```bash
brz run site.yaml login,export --env EMAIL=me@co.com
```

**Dry-run:** `--dry-run` resolves env vars and prints the concrete steps without launching Chrome. Useful to verify YAML before paying the browser tax.

```bash
brz run site.yaml login --dry-run --env EMAIL=me@co.com
```

Success:
```json
{"ok": true, "action": "export", "steps": 3, "duration_ms": 2100, "download": "/tmp/file.csv", "download_size": 51200}
```

Failure (includes `page_elements` with up to 5 similar selectors for agent context):
```json
{"ok": false, "action": "login", "error": "find element ...", "failed_step": 2, "step_type": "click", "screenshot": "/tmp/after.png", "screenshot_before": "/tmp/before.jpg", "page_elements": [{"selector": "button.btn-submit", "tag": "button", "text": "Submit"}]}
```

On failure, `screenshot_before` shows the page BEFORE the failed step ran (JPEG, ~50KB). Compare with `screenshot` (after) to understand what changed. Both are auto-captured with zero overhead on success.

### validate — check workflow syntax

```bash
brz validate <workflow.yaml> [--json]
```

### actions — list available actions

```bash
brz actions <workflow.yaml> [--json]
```

## Workflow YAML Format

```yaml
name: my-workflow
env:                              # optional default env vars
  BASE_URL: https://example.com
debug_screenshots: true           # capture before/after screenshots on failure (default: true, set false for high-frequency workflows)
actions:
  action_name:
    url: ${BASE_URL}/page         # navigate here first (optional — omit to reuse current page)
    force_navigate: true          # force reload even if URL matches current page (optional, default false)
    headed: true                  # with BRZ_HEADED=auto, retry this action headed if headless fails (optional)
    steps:
      - fill: { selector: '#email', value: '${EMAIL}' }
      - click: { selector: '#submit' }
```

### Step Types

| Step | Syntax | Key fields |
|------|--------|------------|
| navigate | `- navigate: "https://..."` | URL string, supports `${ENV}` |
| click | `- click: { selector, text, nth, timeout }` | `selector` required, `text` filters by visible text, `nth` is 0-indexed |
| fill | `- fill: { selector, value, clear }` | `value` supports `${ENV}`, `clear: true` clears first |
| select | `- select: { selector, value, text, timeout }` | Set dropdown value. Auto-detects native `<select>` vs Select2. `text` matches by visible option text. Retries on disabled elements within timeout. Default timeout 5s |
| upload | `- upload: { selector, source }` | `source`: file path or `"result"` (last download) |
| download | `- download: { timeout }` | Must follow a `click` step immediately |
| wait_visible | `- wait_visible: { selector, timeout }` | Wait for element to appear |
| wait_text | `- wait_text: { text, timeout }` | Wait for text on page |
| wait_url | `- wait_url: { match, timeout }` | Wait for URL to contain substring |
| screenshot | `- screenshot: "filename.png"` | Saves to temp directory |
| sleep | `- sleep: { duration: "5s" }` | Go duration string |
| eval | `- eval: "js expression"` | Supports `${ENV}`, runs in page context |

Any step can include `label: "description"` for logging and `optional: true` to continue on failure.

**Click + Download rule:** Always put `download` immediately after the `click` that triggers it. brz registers the download listener before executing the click.

### Timeouts

All timeout values are Go duration strings: `"30s"`, `"2m"`, `"500ms"`. Default: 30 seconds.

### Environment Variables

Values support `${VAR_NAME}` interpolation. Precedence (highest to lowest):
1. `--env KEY=VAL` flags on command line (override everything)
2. Workflow-level `env:` map in YAML (defaults)
3. OS environment (from `.env` files or shell)

Unresolved variables are left as `${VAR_NAME}` (no crash).

## Output and Exit Codes

All commands output JSON when piped or with `--json`. Human-readable one-liners in a terminal.

| Exit code | Meaning |
|-----------|---------|
| 0 | Success |
| 1 | Action step failed (element not found, timeout, etc.) |
| 2 | Workflow error (invalid YAML, missing action, bad path) |
| 3 | Browser error (Chrome not found, launch failed) |

## Session Persistence

brz reuses a Chrome profile at `~/.config/brz/chrome-profile/`. Login cookies survive between runs and across all commands, including between separate `brz run` invocations. Run `login` once, then all subsequent commands (`inspect`, `run`, `screenshot`) see the authenticated session. Use `--ephemeral` for a clean session or `--profile DIR` for a custom location.

## Navigation Behavior

brz optimizes navigation to avoid redundant page loads:

- **Same-URL skip**: If an action's `url` matches the page already loaded, navigation is skipped entirely. Saves 3-5s per redundant load on heavy pages.
- **No-URL continuation**: If an action has no `url` field, it operates on the current page without reloading. Use this for continuation actions that run after another action on the same page.
- **Force navigate**: Set `force_navigate: true` on an action to always reload, even when the URL matches.

```yaml
actions:
  export_magic:
    url: https://store.example.com/admin/pricing   # navigates first time
    steps: [...]
  export_pokemon:
    url: https://store.example.com/admin/pricing   # skips navigation — same URL already loaded
    steps: [...]
  export_continue:
    # no url — operates on whatever page is currently loaded
    steps: [...]
```

## Common Flags

| Flag | Effect |
|------|--------|
| `--json` | Force JSON output (auto when piped) |
| `--headed` | Show browser window (for CAPTCHAs, debugging) |
| `--debug` | Verbose logging + screenshots on failure |
| `--profile DIR` | Custom Chrome profile directory |
| `--ephemeral` | Fresh temp profile, no session reuse |
| `--env KEY=VAL` | Set workflow env var (repeatable, `run` only) |
| `--dry-run` | Print resolved steps without launching Chrome (`run` only) |
| `--tag TAG,...` | Filter inspect elements by tag (`inspect` only) |
| `--name NAME,...` | Filter inspect elements by name attribute (`inspect` only) |
| `--compact` | Compact JSON output with fewer fields (`inspect` only) |
| `--screenshot` | Also capture screenshot (`inspect` only) |
| `--eval "expr"` | Also evaluate JS expression (`inspect` only) |

## Example: Login Workflow

```yaml
name: app-login
actions:
  login:
    url: https://app.example.com/login
    steps:
      - fill: { selector: 'input[name="email"]', value: '${EMAIL}' }
      - fill: { selector: 'input[name="password"]', value: '${PASSWORD}' }
      - click: { selector: 'button[type="submit"]' }
      - wait_url: { match: '/dashboard', timeout: '30s' }
```

```bash
brz run app-login.yaml login --env EMAIL=me@co.com --env PASSWORD=secret
```

## Example: Export CSV

```yaml
name: data-export
actions:
  export:
    url: https://app.example.com/reports
    steps:
      - wait_visible: { selector: '#export-csv', timeout: '10s' }
      - click: { selector: '#export-csv' }
      - download: { timeout: '120s' }
```

```bash
# Run export and get the downloaded file path
brz run data-export.yaml export | jq -r .download
```

## Eval Assertions

Actions can include an `eval:` block that runs after all steps succeed. Evals verify the action produced the right result. If any eval fails, the action result has `ok: false` with details in `eval_errors`.

```yaml
actions:
  export_inventory:
    url: https://example.com/admin/pricing
    steps:
      - click: { selector: '#export-btn' }
      - download: { timeout: '60s' }
    eval:
      - label: "Page loaded OK"
        status_code: 200
      - label: "CSV has data rows"
        download_min_rows: 1
      - label: "CSV has required columns"
        download_has_columns: ["ID", "Name", "Price"]
      - label: "Still on pricing page"
        url_contains: "admin/pricing"
```

| Eval type | Syntax | What it checks |
|-----------|--------|----------------|
| js: | `js: "document.querySelector('.error') === null"` | JS expression returns truthy |
| url_contains: | `url_contains: "admin/pricing"` | Current URL contains substring |
| text_visible: | `text_visible: "Export complete"` | Text appears on page |
| no_text: | `no_text: "error occurred"` | Text does NOT appear on page |
| selector: | `selector: "#success-banner"` | Element exists in DOM |
| status_code: | `status_code: 200` | HTTP status code of last navigation matches |
| download_min_size: | `download_min_size: 50` | Downloaded file is at least N bytes |
| download_min_rows: | `download_min_rows: 1` | Downloaded CSV has at least N data rows |
| download_has_columns: | `download_has_columns: ["ID", "Name"]` | Downloaded CSV has these column headers |

Each eval can include `label:` for logging and `timeout:` (default: 10s) for checks that need to wait.

Evals are immutable verification. An agent can modify steps (selectors, timeouts) to fix a broken workflow, but must never modify eval assertions. If steps pass but evals fail, the action failed.

## The Agent Loop

When automating a new site:

1. **Inspect + screenshot** in one call: `brz inspect <url> --screenshot` to discover selectors and see the page
2. **Filter** if you know what you need: `brz inspect <url> --tag input,button --compact`
3. **Write** a YAML workflow using the selectors from step 1
4. **Dry-run** to verify env vars resolve: `brz run workflow.yaml login --dry-run --env EMAIL=x`
5. **Run** the action: `brz run workflow.yaml <action> --env KEY=VAL`
6. **Chain** actions in one session: `brz run workflow.yaml login,export --env EMAIL=x` (saves a cold-start)

**On failure (exit code 1):**
1. Check `page_elements` in the failure JSON — it includes up to 5 similar selectors from the current page
2. If `page_elements` isn't enough, re-inspect: `brz inspect <url> --tag button` to find the right selector
3. Update selectors in your YAML and add `wait_visible` before clicks on dynamic content
4. Use `--headed` to watch the browser and see what's happening visually
