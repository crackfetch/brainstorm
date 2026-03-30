# TODOS

## `brz prompt` command

**What:** Add a `brz prompt` subcommand that dynamically generates the agent prompt from actual help text, YAML schema, and example files.

**Why:** The static `prompts/agent.md` file will drift from the CLI as commands/flags change. A dynamic generator produces always-accurate output from the source of truth (the code itself).

**Context:** The outside voice in the eng review flagged this as the "obviously correct" long-term solution. We shipped the static file first for immediate value (works without brz installed, embeddable in web UIs, GitHub README). The static file serves as the reference spec for what the command should output. Implementation is ~50-80 lines in `cmd/brz/main.go`, pulling from existing help text and embedding a condensed YAML schema.

**Depends on:** `prompts/agent.md` being stable enough to use as the output template.

## llms.txt

**What:** Ship an `llms.txt` file following the llmstxt.org convention for LLM discoverability.

**Why:** The llms.txt convention is becoming standard for helping LLMs discover what a project does and where to find documentation. Separate from `prompts/agent.md` (which teaches usage), llms.txt is the "front door" for an LLM encountering the project for the first time.

**Context:** Research agent found this is a well-established convention. The file is ~20-30 lines of markdown with H1 (project name), blockquote (summary), and H2 sections linking to docs. Git history shows a prior `llms.txt` was committed (1e4f678) but the file no longer exists on disk.

**Depends on:** Nothing. Can be done independently.
