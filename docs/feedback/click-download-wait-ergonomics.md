# Click / download / wait ergonomics — common-pattern gaps

This doc captures the brz friction surfaced while driving a real-world admin
flow: open a modal, pick a value from a dropdown, click "Export Filtered CSV",
save the resulting download. The same patterns show up on most modern admin
panels — duplicate selectors across page and modal, captcha-gated submits,
click-triggered downloads that leave the tab on `about:blank`. brz already
covered most of this *in principle* (workflows, `download:` step, Select2
auto-detection in `select:`) but the YAML surface didn't have the affordances
a naive author reaches for first.

This PR closes the gaps. Each item is a small, opt-in addition to the YAML
schema — no existing field semantics change, all default-zero behavior is
preserved.

---

## Findings, ranked

| # | Finding | Severity | Status in this PR |
|---|---|---|---|
| 1 | `click <selector>` errors on duplicate-selector matches with no built-in escape | P0 | **Fixed** — `click.visible: true` + `click.nth: -N` |
| 2 | `download:` step ignores save_as and has no return-to-page mechanism | P0 | **Fixed** — `save_as`/`save_to` rename + `return_to: previous \| <url>` |
| 3 | Auto-fill + click on captcha-gated form silently no-ops | P0 | **Fixed** — `wait_enabled` step type |
| 4 | No way to inherit a session from the user's existing browser | P1 | **Out of scope** — needs Chromium keychain crypto, separate PR |
| 5 | `--headed` window does not reliably surface on macOS | P1 | **Out of scope** — needs `osascript` activation, hard to CI-test |
| 6 | No `wait_modal` / scope-to-modal pattern | P2 | **Out of scope** — design discussion needed |
| 7 | No `--connect-url` to attach to a user-launched Chrome | P2 | **Out of scope** — CLI plumbing PR |
| 8 | `eval` step env interpolation has no JS-safe escaping | P2 | **Out of scope** — design discussion (`${{JSON:VAR}}` form) |

Items 1–3 are the ones that block end-to-end workflow authoring; the rest are
real but workarounds exist or they touch surface area too broad for one PR.

---

## What this PR ships

### `click.visible: true` + negative `nth`

```yaml
- click: { selector: 'input[value="Export"]', visible: true, nth: -1 }
```

Filters selector matches to visible elements (`offsetParent != null` and
non-zero bounding rect — same heuristic an interactive devtools picker would
use), then resolves `nth`. Negative indices count from the end: `-1` is last,
`-2` is second-to-last. The fast path (no `visible`/`nth`/`text`) keeps the
original single-`Element()` lookup, so existing workflows are byte-identical.

**Pattern unblocked:** a page-level button and a modal-level submit share a
selector. Before, you had to hand-roll an `eval` step that filtered by
`offsetParent !== null` and clicked the last visible match. Now: one line.

### `download.save_as` / `download.save_to` (alias) + `download.return_to`

```yaml
- download:
    timeout: "60s"
    save_to: "~/Downloads/${NAME}.csv"   # or save_as — both work
    return_to: previous                  # restore page URL after download
```

`save_as` was previously defined in the YAML schema but ignored at execution.
This PR wires it: when set, the captured download is renamed to the target
path. `${ENV}` interpolation and leading `~` expansion both supported. Parent
dirs are auto-created. `save_to` is a sibling alias for authors who reach for
that name first; `save_as` wins when both are set so the older form keeps
its precise behavior.

`return_to: previous` re-navigates the tab to the URL it was on right before
the click that triggered the download. This solves the `about:blank` problem
where subsequent `wait_url` / interaction steps fail because the tab navigated
away during the download. A literal URL is also accepted (with `${ENV}`
interpolation).

**Pattern unblocked:** click triggers a CSV download via
`window.location.href = '/path/to/csv'`. Browser captures the file as an
attachment, leaves the tab at `about:blank`. Before, the next workflow step
silently broke. Now: one line.

### `wait_enabled` step type

```yaml
- fill: { selector: 'input[name="email"]', value: '${EMAIL}' }
- fill: { selector: 'input[name="password"]', value: '${PASSWORD}' }
- wait_enabled: { selector: 'button[type="submit"]', timeout: "120s" }
- click: { selector: 'button[type="submit"]' }
```

Polls until the target element is enabled (no `disabled` attribute, no
`aria-disabled="true"`). Common pattern: forms gated by anti-bot challenges
keep the submit button disabled until the page receives a verification token.
Without `wait_enabled`, the workflow's `click` lands on a still-disabled
button and silently no-ops.

**Pattern unblocked:** captcha-gated login forms. Before, the workflow
would click before the verification finished and then fail at `wait_url`
three minutes later with no diagnostic. Now: `wait_enabled` blocks until
the button is actually clickable.

---

## Backward-compat guards

Every new field is opt-in via its zero value:

- `ClickStep.Visible: false` — fast path unchanged
- `ClickStep.Nth: 0` — fast path unchanged (single-`Element()` lookup)
- `DownloadStep.SaveAs / SaveTo: ""` — file stays in `brz-downloads` temp dir
- `DownloadStep.ReturnTo: ""` — no post-download navigation
- `Step.WaitEnabled: nil` — no new dispatch path

Tests pin all five guards explicitly (`TestClickStep_DefaultsPreserved`,
`TestDownload_NoSaveTarget_KeepsTempPath`, `TestClick_DefaultBehavior_PreservedFirstMatch`).

Positive-`nth` semantics are preserved as well: `nth: 1` continues to mean
"els[1]" (second match in DOM order, the historical 1-indexed-via-zero-elision
behavior). The field comment now documents the surprise so future readers see
it. Rather than fixing the off-by-one in the same PR (which would silently
break callers), the new value is `nth: -N` for from-end indexing — a strictly
additive surface change.

---

## What was discovered along the way

A few things in the brz codebase that are already correct but were undocumented
or unobvious:

- `select:` already auto-detects native `<select>` vs Select2 (jQuery) and
  dispatches the right events. Was not aware of this until reading
  `executor.go:doSelect`. Worth surfacing in `brz prompt` more loudly so
  authors don't reach for `eval` workarounds when `select:` would do.
- `download:` already pre-registers `WaitDownload` BEFORE the triggering
  click, via a look-ahead in the step loop. Solves the rod sequencing
  requirement automatically.
- The agent prompt has a test (`TestPromptContent_DocumentsAllStepTypes`)
  that fails CI when a new step type is added without a corresponding
  doc entry. Excellent guardrail; saved this PR from shipping `wait_enabled`
  without docs.

---

## Suggested follow-up PRs

Listed in priority order:

1. **Cookie import** — `brz cookies import <browser> [--domain D]` to inherit
   a session from the user's existing Chrome/Brave/Edge profile. Needs
   platform-specific keychain crypto for Chromium's encrypted-cookie format.
   The biggest unblocker for one-off scripts where the user already has the
   target site authenticated in their daily browser.
2. **Headed window surfacing on macOS** — best-effort `osascript activate`
   on the launched browser process so the window doesn't sit behind the
   user's existing app windows. Tiny but unblocking.
3. **`--connect-url ws://...`** — attach to a user-launched Chrome
   (`google-chrome --remote-debugging-port=9222`) instead of launching a
   new profile. Equivalent to Playwright's `chromium.connectOverCDP()`.
4. **`wait_modal` / `in_modal:` scope** — declare a modal-aware subtree
   so duplicate-selector resolution doesn't have to be done per-step via
   `visible`/`nth`. Cleaner expression of intent.
5. **`${{JSON:VAR}}` interpolation in `eval`** — guarantees the substituted
   value is a JS-safe string literal regardless of content. Closes a quiet
   correctness footgun.
