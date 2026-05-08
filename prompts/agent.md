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

`page_elements` is scoped recovery context, not a full DOM dump. For failed click targets on `input`, `button`, or `a` selectors, Brainstorm captures nearby compatible action controls across all three tags because sites often swap submit inputs, buttons, and styled links. For submit/button/reset inputs, use `value` as the visible label; buttons and links usually use `text`. Candidate objects may also include `type`, `name`, `placeholder`, and `role`.

The same nearby-element data is also surfaced inline in the `error` string itself, so a human (or LLM) reading just the error sees actionable suggestions without parsing the JSON. Format:

```
action "login", step 2: find element "button.signin": context deadline exceeded
  Nearby visible elements: button.btn-primary (Sign In), input[type=submit] (Submit), a.cta (Continue)
```

Up to 5 visible candidates are listed. Hidden elements are filtered out so the hint never sends you chasing the wrong fix.

On failure, `screenshot_before` shows the page BEFORE the failed step ran (JPEG, ~50KB). Compare with `screenshot` (after) to understand what changed. Both are auto-captured with zero overhead on success.

### validate — check workflow syntax

```bash
brz validate [--strict] [--json] <workflow.yaml>
```

Note: flags must come BEFORE the positional file path. Go's `flag` parser stops at the first positional argument, so `brz validate workflow.yaml --strict` would silently run leniently — always put `--strict` first.

By default `validate` is lenient: unknown fields in YAML are silently dropped (yaml.v3 default). This preserves backward compat for older workflows that may have stale fields.

`--strict` switches the loader to `KnownFields(true)` so any typo is rejected with a YAML line number AND a "Did you mean?" suggestion. Catches the canonical bug where `save_too: ...` (one-character typo of `save_to:`) passes basic validation, then silently fails at runtime because the field goes nowhere.

```bash
$ brz validate --strict workflow.yaml
parse workflow workflow.yaml: yaml: unmarshal errors:
  line 7: unknown field 'save_too' in download step. Did you mean 'save_to'? Valid fields: return_to, save_as, save_to, timeout.
```

The suggestion is computed by Levenshtein distance against the type's declared YAML tags (built once at package init via reflection). When no candidate is close enough, the suggestion is omitted but the valid-fields list is still included.

Use `--strict` in CI / pre-commit; rely on the lenient default for ad-hoc one-offs.

### actions — list available actions

```bash
brz actions <workflow.yaml> [--json]
```

## Diagnostic Commands

### examples — bundled workflow YAML patterns

```bash
brz examples list                        # show available examples + one-line summaries
brz examples cat <name>                  # print one example to stdout
brz examples scaffold <name> [<dir>]     # write the example file into <dir> (default: cwd)
```

The bundled examples are the canonical reference for how to write a workflow. They cover: form login, captcha-gated forms (`wait_enabled`), click-then-download (`click` + `download`), modal disambiguation (`click.visible`+`nth`), download with `save_to` + `return_to`, multi-step uploads, scraping with `eval`, and more.

If you're an LLM writing a workflow, run `brz examples list` first to see what's available, then `brz examples cat <closest-match>` to crib the structure. `scaffold` writes a copy you can edit.

### logs — list recent failure-screenshot artifacts

```bash
brz logs [--follow] [--limit N] [--json]
```

When a workflow step fails, brz writes two artifacts to `$TMPDIR`:
- `<action>_failed_<timestamp>_<step>.png` — page state after failure
- `<action>_before_<step>.jpg` — page state just before the failed step

`brz logs` lists those artifacts newest-first. Use it to find the failure screenshots from your last run without `grep`'ing `/tmp` by hand.

Flags:
- `--follow` — watch `$TMPDIR` and print each new artifact as it appears (useful when running brz in another shell). NDJSON in `--json` mode.
- `--limit N` — max entries (default 20; `0` for unlimited).
- `--json` — array output (auto-enabled when piped).

Each entry includes the parsed action name, step index, kind (failed/before), timestamp, file size, and full path. Pipe through jq to find a specific action's screenshots: `brz logs --json | jq '.[] | select(.action == "login")'`.

`brz status` reports the count at a glance; `brz logs` enumerates them.

### status — print a snapshot of brz's local state

```bash
brz status [--json]
```

Reports:
- Resolved Chrome profile directory and whether a `SingletonLock` file is present (a leftover lock from a crashed launch — brz removes stale locks on next start, but seeing it here lets you debug "why won't my next run start" without launching).
- Running brz-launched Chromium processes: PID, executable path, `--user-data-dir` argument. Detects brz ownership via exact match against the configured profile or the `brz-ephemeral-*` prefix that `--ephemeral` creates.
- Downloads tmpdir occupancy (`<TMPDIR>/brz-downloads`): file count, total bytes, newest timestamp.
- Failure-screenshot count from past failed actions in `<TMPDIR>` (`*_failed_*.png` and `*_before_*.jpg`).

Always exits 0; status is informational. Pipe-friendly via `--json`. Useful when:
- You're not sure if a previous brz run left a Chrome process behind
- A workflow is hitting `SingletonLock` errors and you want to see whether the lock is held
- You want to know how much disk brz is using under `/tmp`
- An LLM agent driving brz needs to inspect its environment without launching anything

On Windows the process scan is skipped (no `ps`); the rest of the report still works.

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
| click | `- click: { selector, text, nth, visible, timeout }` | `selector` required. `text` filters by visible text. `nth` picks a match (`-1` = last; `1` = second match in DOM order — note historical 1-indexed-via-zero-elision behavior). `visible: true` restricts matches to visible elements (offsetParent != null + non-zero rect) — use with `nth: -1` to grab the modal-level submit when a page-level button shares the same selector. |
| fill | `- fill: { selector, value, clear }` | `value` supports `${ENV}`, `clear: true` clears first |
| select | `- select: { selector, value, text, timeout }` | Set dropdown value. Auto-detects native `<select>` vs Select2. `text` matches by visible option text. Retries on disabled elements within timeout. Default timeout 5s |
| upload | `- upload: { selector, source }` | `source`: file path or `"result"` (last download) |
| download | `- download: { timeout, save_as, save_to, return_to }` | Must follow a `click` step immediately. `save_as` / `save_to` are aliases — write the captured download to a target path (supports `${ENV}` and leading `~`; parent dirs created). `return_to: previous` re-navigates the tab to the URL it was on before the click (use after click-triggered downloads, which leave the tab at `about:blank` and break subsequent `wait_url`). `return_to: "https://..."` navigates to a literal URL. |
| wait_visible | `- wait_visible: { selector, timeout }` | Wait for element to appear |
| wait_text | `- wait_text: { text, timeout }` | Wait for text on page |
| wait_url | `- wait_url: { match, timeout }` | Wait for URL to contain substring |
| wait_enabled | `- wait_enabled: { selector, timeout }` | Wait for element to be enabled (no `disabled` attribute, no `aria-disabled="true"`). Common pattern: forms gated by anti-bot challenges keep the submit button disabled until verification — put `wait_enabled` between `fill` and `click` so brz blocks until the element is interactable instead of clicking a disabled button (which silently no-ops). |
| handoff | `- handoff: { message, wait_url \| wait_eval, timeout }` | Pause the workflow for a human takeover. Switches the browser to headed mode (relaunching if currently headless and re-navigating to the URL the user was on), prints the message + current URL to stderr, then blocks until the resume signal fires: `wait_url` matches a substring of the URL, or `wait_eval` returns JS-truthy (the expression must be a function — `() => ...` or `function() { return ... }`; we wrap it in `Boolean(expr())` so 1 / "ready" / DOM-node returns all resume). Default timeout 10m. Set exactly one of `wait_url` / `wait_eval`. Use this instead of a long `wait_url` when you want the intent ("stop and let the human drive") to be explicit and the window to be guaranteed visible. |
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
1. Check `page_elements` in the failure JSON — it includes up to 5 similar selectors from the current page. Prefer matching the intended label from `text` or input `value`, not just selector shape.
2. If `page_elements` isn't enough, re-inspect: `brz inspect <url> --tag button` to find the right selector
3. Update selectors in your YAML and add `wait_visible` before clicks on dynamic content
4. Use `--headed` to watch the browser and see what's happening visually
