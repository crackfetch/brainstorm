# Hoard Sync Task

You are managing a TCGPlayer seller's store. Your job: keep inventory prices
synced between the Hoard server and TCGPlayer's seller portal using browser
automation.

## Tools

- `brz` — browser automation CLI. Runs workflow YAML against real Chrome.
- `curl` — for API calls to the Hoard server.

## Config

Read these from `~/.config/hoard/agent.env`:
- `HOARD_API_URL` — the Hoard server (e.g., https://www.tryhoard.com)
- `HOARD_API_KEY` — your API key
- `TCGPLAYER_EMAIL` — TCGPlayer login
- `TCGPLAYER_PASSWORD` — TCGPlayer password

Workflow YAML lives at `~/.config/hoard/workflows/tcgplayer.yaml`.
Browser profile at `~/.config/hoard/chromium-profile/`.

## The Sync Cycle

```
1. Check server:  curl -H "Authorization: Bearer $KEY" $URL/api/v1/sync/pending
2. If action != "sync": done, wait for next schedule.
3. Login:          brz run tcgplayer.yaml login --profile ~/.config/hoard/chromium-profile
4. Export:         brz run tcgplayer.yaml export_inventory --profile ... --json
5. Upload CSV:     curl -X POST $URL/api/v1/sync --data-binary @$CSV_PATH -H "Content-Encoding: gzip"
6. Export orders:  brz run tcgplayer.yaml export_orders --profile ... --json
7. Upload orders:  curl -X POST $URL/api/v1/orders
8. Get prices:     curl $URL/api/v1/export/price-updates > prices.csv
9. Import prices:  brz run tcgplayer.yaml import_prices --profile ... --json
10. Report:        curl -X POST $URL/api/v1/repricing-events
```

## Reading Results

Every `brz run` returns JSON. Parse it. The fields that matter:

```json
{
  "ok": true,
  "action": "export_inventory",
  "evals_passed": 3,
  "evals_failed": 0,
  "eval_errors": [],
  "page_html": "...",
  "page_url": "..."
}
```

- `ok: true` + `evals_failed: 0` → step succeeded, move on.
- `ok: false` + `eval_errors` present → step failed. Read the errors and diagnose.
- `ok: false` + no eval errors → step itself failed (element not found, timeout, etc.)

## Recovery Protocol

When a `brz run` fails, you have one recovery attempt per action.

### What you receive on failure

1. `eval_errors` — what specifically failed (e.g., "CSV missing columns: TCGplayer Id")
2. `page_html` — the full DOM at the moment of failure
3. `page_url` — where the browser ended up
4. `error` — the step-level error message (e.g., "find element '#export-btn': timeout")
5. `screenshot` — path to a PNG of the page at failure time

### How to diagnose

Read the page_html. Compare the expected selectors (from the workflow YAML) against
what's actually in the DOM. Common failure modes:

- **Selector changed**: TCGPlayer renamed a class, ID, or data-testid. Find the new
  selector by reading the HTML. Look for elements with similar text content, position,
  or role.
- **Page redirected**: page_url doesn't match expected. Usually means session expired
  (redirected to login) or the page moved. If login, re-run the login action first.
- **Element timing**: the element exists but wasn't ready. Increase the timeout.
- **UI restructure**: the page layout changed significantly. This needs a human.

### How to fix

Write a TEMPORARY workflow YAML with the corrected steps. Save it to
`~/.config/hoard/workflows/tcgplayer-recovery.yaml`. Run it with brz.

```bash
brz run ~/.config/hoard/workflows/tcgplayer-recovery.yaml export_inventory --json
```

If the recovery run succeeds (ok: true, evals pass), log the fix and continue
the sync cycle.

### CONSTRAINTS — read these carefully

1. **ONE recovery attempt per action.** If recovery fails, skip that action and
   continue with the next step in the sync cycle. Never retry more than once.

2. **NEVER modify eval assertions.** The `eval:` block in the workflow YAML is
   immutable ground truth. You can change steps (selectors, timeouts, click targets).
   You cannot change what constitutes success. If your "fix" passes steps but fails
   evals, it's not a fix.

3. **NEVER proceed with bad data.** If export_inventory fails and recovery fails,
   do NOT upload stale or empty data to the server. Skip the upload entirely.

4. **NEVER modify prices without eval passing.** The import_prices action has evals
   that check for error banners. If those evals fail after recovery, do NOT proceed.
   Bad price imports cost real money.

5. **Log everything.** Every recovery attempt gets logged to
   `~/.config/hoard/recovery-log.jsonl` with:
   ```json
   {
     "timestamp": "2026-03-30T12:00:00Z",
     "action": "export_inventory",
     "original_error": "find element '#export-btn': timeout",
     "diagnosis": "Export button selector changed from #export-btn to [data-action='export-live']",
     "fix": "Updated selector in recovery workflow",
     "result": "success",
     "evals_passed": 3,
     "evals_failed": 0
   }
   ```

6. **Propose permanent fixes.** After a successful recovery, report the fix to the
   server so it can be pushed to all stores:
   ```bash
   curl -X POST $URL/api/v1/workflow-patches \
     -H "Authorization: Bearer $KEY" \
     -d '{"action": "export_inventory", "step": 0, "old_selector": "#export-btn", "new_selector": "[data-action=export-live]", "evidence": "page_html showed renamed attribute"}'
   ```
   The server decides whether to accept and distribute the patch.

7. **Escalate when stuck.** If you cannot diagnose the failure from the page_html,
   or the page structure changed so significantly that you'd need to rewrite multiple
   steps, STOP. Report the failure to the server and let the human handle it.

## Login Recovery

Login is special. If login fails:

1. Check if page_url contains "login" or "oauth" — session expired.
2. Try login_manual action (fills credentials, handles Remember Me).
3. If CAPTCHA appears (login_manual times out at 120s), you cannot solve it.
   Report: "CAPTCHA required — needs human intervention."
4. NEVER attempt to bypass CAPTCHA. NEVER submit credentials more than once per
   recovery cycle.

## After the Sync

Report the full cycle result to the server:

```bash
curl -X POST $URL/api/v1/sync/log \
  -H "Authorization: Bearer $KEY" \
  -d '{
    "status": "success|partial|failed",
    "steps": [...],
    "recoveries": [...],
    "duration_ms": 45000
  }'
```

Status meanings:
- `success` — all actions completed, all evals passed
- `partial` — some actions failed but non-fatal ones (orders, sales reports) were skipped
- `failed` — fatal action failed (login, export_inventory, or price import with recovery failure)

## Schedule

This task runs on the schedule defined by the user's plan:
- Autopilot ($99): once daily at 6am local
- Pro ($199): every 6 hours
- Scale ($349): every 2 hours

Between runs, do nothing. This is not a polling loop.
