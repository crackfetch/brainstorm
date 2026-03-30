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
  main.go                  7 commands: run, inspect, screenshot, eval, validate, actions, version
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
prompts/                   LLM agent prompt (agent.md — universal, works with any LLM)
docs/                      Architecture, getting started, workflow YAML spec
```

## Key Patterns

- **Discovery-first**: inspect/screenshot/eval commands let LLMs explore pages before writing workflows.
- **Workflow-driven**: All browser automation is defined in YAML. No site-specific code in the binary.
- **LLM-friendly**: JSON output when piped, semantic exit codes (0/1/2/3), rich --help with schemas.
- **Session persistence**: Reuses Chrome profile between invocations. Login cookies survive across all commands. Stale SingletonLock files are auto-recovered.
- **Auto-headed**: `BRZ_HEADED=auto` starts headless, escalates to headed when actions marked `headed: true` fail (e.g., expired cookies). On failure, captures page HTML/URL in result and keeps browser open in headed mode.
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
| `brz version` | — | Version string |

## Testing

- `workflow/` — unit tests for YAML parsing, env interpolation, timeout parsing, eval assertions
- Browser E2E — manual only (`brz inspect https://example.com --headed`)
