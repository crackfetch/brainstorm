# Brainstorm

Generic YAML-driven browser automation CLI (`brz`). Built with Go + [rod](https://github.com/go-rod/rod).

## Build

```bash
go build -o brz ./cmd/brz
```

## Test

```bash
go test ./...
```

## Run

```bash
# Discovery commands (no workflow needed)
brz inspect https://example.com/login
brz screenshot https://example.com
brz eval https://example.com "document.title"

# Workflow commands
brz validate workflows/examples/form-login.yaml
brz run workflows/examples/form-login.yaml login --headed --env EMAIL=test@example.com
```

## Architecture

```
cmd/brz/                   CLI entry point: subcommands, flags, JSON/TTY output
  main.go                  8 commands: run, inspect, screenshot, eval, validate, actions, prompt, version
                           Shared browserSetup helper for all browser commands
internal/config/           ENV loading from ~/.config/brz/agent.env + .env
workflow/                  Public package — importable by other Go programs
  types.go                 Workflow/Action/Step/EvalAssert structs (YAML schema)
  loader.go                Load(), LoadFromBytes(), InterpolateEnv()
  executor.go              NewExecutor(), Start(), NavigateTo(), RunAction()
  eval.go                  Post-action eval assertions (JS, URL, text, selector, download checks)
  result.go                ActionResult struct (JSON-serializable, includes eval results)
  inspect.go               InspectResult, ElementInfo, InspectJS (embedded JS extractor)
  options.go               Functional options: WithHeaded, WithAutoHeaded, WithDebug, WithProfileDir
workflows/examples/        Example workflow definitions (YAML)
prompts/                   LLM agent prompt (agent.md — embedded into binary via go:embed)
docs/                      Architecture, getting started, workflow YAML spec
```

## Key Patterns

- **Discovery-first**: inspect/screenshot/eval commands let LLMs explore pages before writing workflows.
- **Workflow-driven**: All browser automation is defined in YAML. No site-specific code in the binary.
- **LLM-friendly**: JSON output when piped, semantic exit codes (0/1/2/3), rich --help with schemas.
- **Session persistence**: Reuses Chrome profile between invocations. Login cookies survive across all commands. Stale SingletonLock files are auto-recovered.
- **Auto-headed**: `BRZ_HEADED=auto` starts headless, escalates to headed when actions marked `headed: true` fail (e.g., expired cookies). On failure, captures page HTML/URL in result and keeps browser open in headed mode.
- **Eval assertions**: Post-action verification (9 types: js, url_contains, text_visible, no_text, selector, status_code, download_min_size, download_min_rows, download_has_columns).
- **Debug screenshots**: Before/after JPEG screenshots on step failure. Ring buffer in memory, zero disk I/O on success. Opt out with `debug_screenshots: false`.
- **Configurable viewport**: Default 1280x900. Override at workflow or action level.
- **Native dropdown support**: `select` step auto-detects native `<select>` vs Select2 dropdowns.
- **System Chrome first**: Uses existing Chrome installation. Falls back to auto-download.
- **Stealth**: Masks navigator.webdriver, disables AutomationControlled, real User-Agent.
- **Public Go API**: `workflow/` package is importable. `hoard-agent` uses it as a library.

## Commands

| Command | Input | Output |
|---------|-------|--------|
| `brz inspect <url>` | URL | JSON: interactive elements with CSS selectors |
| `brz screenshot <url>` | URL | JSON: file path + size |
| `brz eval <url> <js>` | URL + JS | JSON: eval result |
| `brz run <wf> <action>` | Workflow YAML | JSON: action result + download path |
| `brz validate <wf>` | Workflow YAML | JSON: action/step counts |
| `brz actions <wf>` | Workflow YAML | JSON: action list with URLs |
| `brz prompt` | — | Markdown: full LLM agent prompt |
| `brz version` | — | Version string |

## Testing

- `workflow/` — unit tests for YAML parsing, env interpolation, timeout parsing, eval assertions, viewport resolution, select step, debug screenshots, drift detection (struct reflection ensures agent.md documents all step/eval types)
- `cmd/brz/` — prompt content tests with struct reflection drift detection
- Browser E2E — manual only (`brz inspect https://example.com --headed`)
