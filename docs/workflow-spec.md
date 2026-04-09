# brz Workflow YAML Specification

This document defines the complete YAML schema for brz workflow files. A workflow file tells brz what browser actions to perform.

**Tip:** Use `brz inspect <url>` to discover the CSS selectors, input names, and element types on a page before writing your workflow. The selectors from inspect output work directly in `click`, `fill`, and other step types.

## Top-Level Structure

```yaml
name: string              # Required. Unique name for this workflow.
env:                      # Optional. Default environment variables.
  KEY: value
viewport:                 # Optional. Browser viewport size. Default: 1280x900.
  width: 1280
  height: 900
debug_screenshots: true   # Optional. Capture before/after screenshots on failure. Default: true.
actions:                  # Required. Named actions the agent can execute.
  action_name:
    url: string           # Optional. Navigate here before running steps.
    headed: bool          # Optional. When BRZ_HEADED=auto, run this action headed if headless fails.
    viewport:             # Optional. Override workflow-level viewport for this action.
      width: 375
      height: 812
    steps: []             # Required. Ordered list of steps to execute.
    eval: []              # Optional. Post-action assertions to verify success.
```

## Steps

Each step performs one browser operation. Steps execute in order. If a step fails, the action stops and returns an error.

### navigate

Navigate to a URL. Waits for the page to finish loading.

```yaml
- navigate: "https://example.com/page"
```

Supports `${ENV_VAR}` interpolation.

### click

Click an element.

```yaml
# Basic: click first matching element
- click: { selector: '#submit-btn' }

# By text: click element matching selector AND containing text
- click: { selector: 'button', text: 'Sign In' }

# By index: click the Nth matching element (0-indexed)
- click: { selector: '.item', nth: 2 }

# With timeout
- click: { selector: '#slow-btn', timeout: '60s' }
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `selector` | string | required | CSS selector |
| `text` | string | — | Filter by visible text content |
| `nth` | int | 0 | 0-indexed position among matches |
| `timeout` | string | "30s" | Max wait time (e.g. "10s", "2m") |

### fill

Type into an input field.

```yaml
# Basic
- fill: { selector: 'input[name="email"]', value: 'user@example.com' }

# With env var interpolation
- fill: { selector: 'input[name="password"]', value: '${PASSWORD}' }

# Clear field first, then type
- fill: { selector: '#search', value: 'new query', clear: true }
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `selector` | string | required | CSS selector for the input |
| `value` | string | required | Text to type (supports `${ENV_VAR}`) |
| `clear` | bool | false | Clear existing text before typing |

### select

Set a dropdown value. Auto-detects native `<select>` elements vs Select2 (jQuery) dropdowns and triggers the appropriate change events.

```yaml
# By option value
- select: { selector: '#category', value: '3' }

# By visible text
- select: { selector: '#category', text: 'Pokemon' }

# With env var and timeout
- select: { selector: '#dropdown', value: '${CATEGORY_ID}', timeout: '10s' }
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `selector` | string | required | CSS selector for the `<select>` element |
| `value` | string | — | Option value to select (supports `${ENV_VAR}`) |
| `text` | string | — | Match by visible option text instead of value |
| `timeout` | string | "5s" | Max wait for element to appear |

One of `value` or `text` is required. If the value/text doesn't match any option, the step fails with a clear error. Disabled dropdowns also fail.

### upload

Set a file on a `<input type="file">` element.

```yaml
# Upload a specific file
- upload: { selector: 'input[type="file"]', source: '/path/to/file.csv' }

# Upload the result from the last download step
- upload: { selector: 'input[type="file"]', source: 'result' }
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `selector` | string | required | CSS selector for file input |
| `source` | string | required | File path, or `"result"` to use last download |

### download

Wait for a browser-initiated file download to complete. **Important**: the action that triggers the download (usually a `click`) must be the step immediately before this one. brz automatically registers the download listener before executing the click.

```yaml
- click: { selector: '#export-btn' }
- download: { timeout: '60s' }
```

After a download completes, the file content is available as the executor's `LastResult` and the file path as `LastDownload`. These can be used by subsequent `upload` steps with `source: "result"`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `timeout` | string | "30s" | Max wait for download to complete |
| `save_as` | string | — | Optional filename pattern |

### wait_visible

Wait for an element to become visible on the page.

```yaml
- wait_visible: { selector: '#results-table', timeout: '10s' }
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `selector` | string | required | CSS selector |
| `timeout` | string | "30s" | Max wait time |

### wait_text

Wait for specific text content to appear anywhere on the page.

```yaml
- wait_text: { text: 'Upload complete', timeout: '60s' }
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `text` | string | required | Text to search for |
| `timeout` | string | "30s" | Max wait time |

### wait_url

Wait for the page URL to contain a specific string.

```yaml
- wait_url: { match: 'dashboard', timeout: '30s' }
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `match` | string | required | Substring to match in the URL |
| `timeout` | string | "30s" | Max wait time |

### screenshot

Capture a screenshot. Saved to the system temp directory.

```yaml
- screenshot: "after_login.png"
```

### sleep

Wait a fixed duration.

```yaml
- sleep: { duration: '5s' }
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `duration` | string | required | How long to wait (e.g. "5s", "1m") |

### eval

Execute JavaScript on the page.

```yaml
- eval: "document.querySelector('#hidden-field').value = 'test'"
```

Supports `${ENV_VAR}` interpolation.

### label

Any step can include a `label` for logging. Labels appear in debug output and error messages.

```yaml
- label: "Click the export button"
  click: { selector: '#export' }
```

## Environment Variable Interpolation

Any string value in a step supports `${VAR_NAME}` substitution. Resolution order:

1. Workflow-level `env` map (defined in the YAML)
2. OS environment variables (from `.env` file or shell)

If a variable is not found, the `${VAR_NAME}` placeholder is left as-is (no crash).

```yaml
name: my-workflow
env:
  BASE_URL: https://example.com
actions:
  login:
    url: ${BASE_URL}/login
    steps:
      - fill: { selector: '#email', value: '${USER_EMAIL}' }
```

## Timeouts

All timeout values are Go duration strings: `"30s"`, `"2m"`, `"500ms"`. If omitted or unparseable, defaults to 30 seconds.

## Optional Steps

Any step can include `optional: true`. If an optional step fails, execution continues and the action can still succeed. Useful for elements that may or may not be present (dismiss buttons, "Remember Me" checkboxes, cookie banners).

```yaml
- label: "Dismiss popup if present"
  click: { selector: '.modal-close', timeout: '3s' }
  optional: true
```

## Eval Assertions

Actions can include an `eval:` block that runs after all steps succeed. Evals verify the action produced the right result.

```yaml
actions:
  export:
    steps:
      - click: { selector: '#export-btn' }
      - download: { timeout: '60s' }
    eval:
      - label: "Page loaded OK"
        status_code: 200
      - label: "CSV has data"
        download_min_rows: 1
      - label: "Has required columns"
        download_has_columns: ["ID", "Name", "Price"]
      - label: "Still on export page"
        url_contains: "export"
```

| Eval type | Syntax | What it checks |
|-----------|--------|----------------|
| `js` | `js: "expression"` | JS expression returns truthy |
| `url_contains` | `url_contains: "path"` | Current URL contains substring |
| `text_visible` | `text_visible: "text"` | Text appears on page |
| `no_text` | `no_text: "text"` | Text does NOT appear on page |
| `selector` | `selector: "#el"` | Element exists in DOM |
| `status_code` | `status_code: 200` | HTTP status code matches |
| `download_min_size` | `download_min_size: 50` | File at least N bytes |
| `download_min_rows` | `download_min_rows: 1` | CSV has at least N data rows |
| `download_has_columns` | `download_has_columns: [...]` | CSV has these column headers |

Each eval can include `label:` and `timeout:` (default 10s). If any eval fails, the action result has `ok: false` with details in `eval_errors`.

## Error Handling

- If any step fails, the entire action stops and returns an error.
- On failure, both a before and after screenshot are captured (before shows page state before the step, after shows the result).
- Before screenshots are held in memory during execution (zero disk I/O on success) and only written to disk when a failure occurs.
- Set `debug_screenshots: false` in the workflow to disable before screenshots for high-frequency workflows.
- The calling application decides what to do with errors (retry, skip, abort).

## Click + Download Sequencing

Browser downloads require the download listener to be registered *before* the click that triggers the download. brz handles this automatically: when a `click` step is immediately followed by a `download` step, the download listener is pre-registered before the click executes.

Always structure your workflow as:
```yaml
- click: { selector: '#download-btn' }
- download: { timeout: '60s' }
```

Do **not** put other steps between the click and the download.
