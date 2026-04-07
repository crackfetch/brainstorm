# TODOS

## Use real browser User-Agent instead of hardcoded string

**What:** Replace the hardcoded `defaultUserAgent` (Chrome 131 / macOS) in `workflow/executor.go` with the actual UA from the running Chrome instance.

**Why:** The frozen UA is a bot-detection fingerprint. It sends Chrome 131 when rod runs Chrome 136+, and sends macOS on Linux/Windows. Sites doing version-matching UA checks will flag this.

**Context:** Rod exposes `browser.GetVersion()` which returns the real UA. Set it once after `Start()` and use it for all pages. The hardcoded string is set in 3 places (executor.go lines 165, 204, 243) that should all use the same dynamic value.

**Depends on:** Nothing. Small standalone change.
