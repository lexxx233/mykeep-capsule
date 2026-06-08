# mykeep integrations — automatic capture

mykeep deliberately runs no LLM: the agent does the reasoning. Its one weak point is
**silent under-retention** — if the agent forgets to call `retain`, memory quietly never
gets written. These recipes fix that by making the *trigger* automatic (plumbing) while
the *judgment* (what's worth keeping, what to promote) stays the agent's.

Two pieces:

- **Capture** — a per-turn hook calls `mykeep capture`, logging each raw turn as a
  low-tier, mechanically-deduped `capture` memory. Always-on safety net.
- **Distill** — an auto-triggered nudge tells the agent to periodically promote the durable
  captures into curated `mental_model`s (folding/deleting the raw ones via `supersedes`).

Captures are **hidden from `recall`/`reflect` by default** — they're a substrate, surfaced
only after distillation or via `recall {"include_captures": true}`.

| Host | Recipe |
|---|---|
| **Claude Code** | [`claude-code/`](claude-code/install.md) — `UserPromptSubmit` + `Stop` hooks |
| **Any shell-driven agent** | [`generic/mykeep-wrap.sh`](generic/mykeep-wrap.sh) — captures each stdin turn |

All recipes are non-fatal by design: if mykeep is stopped, capture is silently skipped and
the turn proceeds normally. They require `mykeep` on `PATH` (a running server) and `jq`.
