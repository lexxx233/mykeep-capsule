#!/usr/bin/env bash
# mykeep auto-capture — a Claude Code UserPromptSubmit hook.
#
# Logs each user turn to mykeep as a raw, mechanically-deduped "capture" memory, so
# retention never depends on the agent remembering to call retain. It is deliberately
# NON-FATAL and SILENT: it never blocks a turn and never prints to Claude's context.
#
# Requires: `mykeep` on PATH (a running `mykeep serve`/GUI) and `jq`.
# Env: MYKEEP_BANK (default: project dir name).
set -uo pipefail

payload="$(cat)"
prompt="$(printf '%s' "$payload" | jq -r '.prompt // empty' 2>/dev/null || true)"
[ -z "$prompt" ] && exit 0

cwd="$(printf '%s' "$payload" | jq -r '.cwd // "."' 2>/dev/null || echo .)"
bank="${MYKEEP_BANK:-$(basename "$cwd")}"

# Fire-and-forget. `|| true` + redirect → a stopped mykeep can never block the prompt,
# and no output leaks into Claude's context (UserPromptSubmit stdout is injected).
mykeep capture --bank "$bank" --role user -- "$prompt" >/dev/null 2>&1 || true
exit 0
