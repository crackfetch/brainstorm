# brz Workflow YAML Specification

This document defines the complete YAML schema for brz workflow files. A workflow file tells brz what browser actions to perform.

**Tip:** Use `brz inspect <url>` to discover the CSS selectors, input names, and element types on a page before writing your workflow. The selectors from inspect output work directly in `click`, `fill`, and other step types.

## Top-Level Structure

```yaml
name: string        # Required. Unique name for this workflow.
env:                # Optional. Default environment variables.
  KEY: value
actions:            # Required. Named actions the agent can execute.
  action_name:
    url: string     # Optional. Navigate here before running steps.
    headed: bool    # Optional. When BRZ_HEADED=auto, run this action headed if headless fails.
    steps: []       # Required. Ordered list of steps to execute.
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

## Error Handling

- If any step fails, the entire action stops and returns an error.
- On failure, a screenshot is automatically captured (saved to temp directory).
- The calling application decides what to do with errors (retry, skip, abort).

## Click + Download Sequencing

Browser downloads require the download listener to be registered *before* the click that triggers the download. brz handles this automatically: when a `click` step is immediately followed by a `download` step, the download listener is pre-registered before the click executes.

Always structure your workflow as:
```yaml
- click: { selector: '#download-btn' }
- download: { timeout: '60s' }
```

Do **not** put other steps between the click and the download.
