#!/usr/bin/env bash
# mykeep generic capture wrapper — for any agent driven from a shell loop (custom
# REPLs, CI agents, chat clients with a shell tool) that lacks a hook system.
#
# Usage:  mykeep-wrap.sh <your-agent-command> [args...]
# It captures each stdin line as a raw turn, then pipes the line to your agent.
#
# Env: MYKEEP_BANK (default: "default").
set -uo pipefail
bank="${MYKEEP_BANK:-default}"

while IFS= read -r line; do
  mykeep capture --bank "$bank" --role user -- "$line" >/dev/null 2>&1 || true
  printf '%s\n' "$line"
done | "$@"
