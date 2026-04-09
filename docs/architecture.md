# Architecture

## Overview

```
┌─────────────────────────────────────────────────────┐
│  brz binary                                         │
│                                                     │
│  ┌──────────────────────────────────────────┐       │
│  │ Discovery Commands (no workflow needed)  │       │
│  │  inspect  screenshot  eval               │       │
│  └──────────────┬───────────────────────────┘       │
│                 │                                    │
│  ┌──────────────┴───────────────────────────┐       │
│  │ Executor (shared browser engine)         │       │
│  │  Start() → NavigateTo() or RunAction()   │──────►│── Chrome/Chromium
│  │  Stealth, profiles, page management      │       │
│  └──────────────┬───────────────────────────┘       │
│                 │                                    │
│  ┌──────────────┴───────────────────────────┐       │
│  │ Workflow Commands (YAML-driven)          │       │
│  │  run  validate  actions                  │       │
│  └──────────────────────────────────────────┘       │
│                                                     │
│  ┌──────────┐  ┌──────────┐                         │
│  │ Config   │  │ CLI      │                         │
│  │ .env     │  │ Output   │  JSON / human-readable  │
│  └──────────┘  └──────────┘                         │
└─────────────────────────────────────────────────────┘
```

## Two Modes of Operation

### 1. Discovery Mode (no workflow needed)

```
brz inspect <url>          brz screenshot <url>       brz eval <url> <js>
        │                          │                          │
        ▼                          ▼                          ▼
    Executor.Start() + NavigateTo(url)  (shared browser setup)
        │                          │                          │
        ▼                          ▼                          ▼
  page.Eval(InspectJS)     page.Screenshot()         page.Eval(userJS)
  extract elements          save PNG to file          return JS result
        │                          │                          │
        ▼                          ▼                          ▼
  InspectResult JSON       {file, size} JSON         {result} JSON
```

Discovery commands launch a browser, navigate to a URL, do one thing, and return JSON. They share the same Executor for browser lifecycle, stealth, and session persistence.

### 2. Workflow Mode (YAML-driven)

```
Workflow YAML          Environment / --env flags
     │                     │
     ▼                     ▼
  loader.Load() ──► Executor.RunAction() ──► Chrome
                         │                      │
                         │                Website pages
                         │                      │
                         ▼                      ▼
                  ActionResult ◄────── Downloaded files
                  (JSON output)        Screenshots
```

Workflow commands parse YAML, resolve `${ENV_VAR}` placeholders, then execute steps sequentially against real pages. Each step can click, fill, download, wait, or run JavaScript.

## Packages

### `workflow/` (public, importable)

The core engine. Six files:

| File | Purpose |
|------|---------|
| `types.go` | Go structs matching the YAML schema (Workflow, Action, Step, ClickStep, etc.) |
| `loader.go` | YAML parser, validation, `${ENV_VAR}` interpolation |
| `executor.go` | Browser lifecycle (Start, Close), page management (NavigateTo, RunAction), stealth |
| `result.go` | ActionResult struct returned by RunAction |
| `inspect.go` | InspectResult/ElementInfo types, InspectJS (embedded DOM extraction JavaScript) |
| `options.go` | Functional options: WithHeaded, WithAutoHeaded, WithDebug, WithProfileDir |

**Key Executor methods:**

| Method | Used by | Purpose |
|--------|---------|---------|
| `Start()` | All commands | Launch browser with stealth settings, auto-recover stale SingletonLock |
| `Close()` | All commands | Shut down browser |
| `NavigateTo(url)` | inspect, screenshot, eval | Create page, inject stealth, navigate |
| `RunAction(name)` | run | Execute a named workflow action, auto-escalate to headed if needed |
| `WaitOnFailure()` | run | Keep browser open on failure in headed mode for debugging |
| `SetEnv(key, val)` | run | Inject env vars for `${VAR}` interpolation |
| `Page()` | inspect, eval | Access the underlying rod page for direct operations |

**Inspect JavaScript (`InspectJS`):**

Embedded as a Go constant. Extracts interactive DOM elements and generates CSS selectors. Two modes:
- **Default:** Returns only actionable elements (inputs, buttons, links, selects, textareas, file inputs, `[role="button"]`, contenteditable). Includes hidden elements (CSRF tokens, hidden inputs).
- **Full (`--full`):** Returns all visible elements, capped at 500 to prevent token bloat.

Selector generation priority: `#id` > `tag[name="x"]` > `tag[type="x"]` > `tag.class` > `tag[aria-label="x"]` > `tag:nth-of-type(n)` > `tag`.

### `internal/config/`

Loads environment variables from:
1. `~/.config/brz/agent.env` (stable location)
2. `.env` in working directory (local override)

Three config values: `BRZ_HEADED` (`"1"`, `"auto"`, or empty), `BRZ_DEBUG`, `BRZ_PROFILE_DIR`.

### `cmd/brz/`

CLI entry point with 7 subcommands. Key design:

- **`addBrowserFlags(fs)`** registers common flags (--json, --headed, --debug, --profile, --ephemeral) on any FlagSet
- **`startBrowser(bf)`** creates and starts an Executor from flags + config. Returns `(exec, useJSON)`
- **TTY detection** via `golang.org/x/term`: JSON when piped, human-readable in terminal
- **Semantic exit codes**: 0 success, 1 step failed, 2 workflow error, 3 browser error

## Browser Automation (rod)

brz uses [rod](https://github.com/go-rod/rod), a Go library for the Chrome DevTools Protocol.

**Why rod:**
- Pure Go, no CGO — cross-compiles to any platform
- Built-in Chromium auto-downloader
- System Chrome detection (zero download for most users)
- Persistent browser profiles via `UserDataDir`

**Stealth measures:**
- `--disable-blink-features=AutomationControlled` launch flag
- `navigator.webdriver` property masked via JavaScript injection
- Dynamic User-Agent from the running Chrome instance (via CDP `Browser.getVersion`)
- Persistent profile reuses legitimate session cookies

## Session Persistence

All commands share the same Chrome profile at `~/.config/brz/chrome-profile/`. If Chrome previously crashed, brz auto-recovers by detecting and removing stale `SingletonLock` files (checks if the owning PID is dead via signal 0). This means:

1. `brz run site.yaml login --headed` (log in with visible browser, solve CAPTCHA)
2. `brz inspect https://site.com/dashboard` (uses cookies from step 1)
3. `brz run site.yaml export` (still authenticated)

The `--ephemeral` flag creates a temp profile for clean sessions.

## Go Library Usage

The `workflow/` package is a public API. Other Go programs import it:

```go
import "github.com/crackfetch/brainstorm/workflow"

// Run a workflow action
w, _ := workflow.Load("my-workflow.yaml")
exec := workflow.NewExecutor(w, workflow.WithHeaded(true))
exec.Start()
defer exec.Close()
result := exec.RunAction("export")

// One-shot page navigation (no workflow)
exec := workflow.NewExecutor(nil)
exec.Start()
defer exec.Close()
exec.NavigateTo("https://example.com")
page := exec.Page()
// Use rod API directly on page
```

This is how `hoard-agent` uses brz: it imports the workflow package, bundles a TCGplayer workflow YAML, and adds Hoard-specific orchestration (API client, polling loop, CSV parsing) on top.
