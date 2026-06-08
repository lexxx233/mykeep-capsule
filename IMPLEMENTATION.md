# Implementation status

Maps the `PLAN.md` milestones to what is built, tested, and verified — and what is
honestly deferred. The v1 **core is complete and runs end-to-end**; deferred items are
enhancements and hardening, not gaps in the happy path.

## Done & verified

| Area | Status |
|---|---|
| **M0 scaffolding** | `internal/paths` (USB-relative resolution + host fallback), `cmd/mykeep`, `Makefile`, GitHub CI, **no-CGo guard**, six-target cross-compile (~15–17 MB each) |
| **M1 config / secret** | argon2id KEK → wrapped random DEK → AES-256-GCM; full-width KDF params bound as AEAD AAD (D7); `KeyStore` with zeroize; password-before-serving launch flow; no passphrase policy + NO-RECOVERY warning |
| **M2 storage / encryption (D13/D19)** | whole-DB AES-256-GCM via SQLite `Serialize`/`Deserialize` (no plaintext on disk); **debounced, off-lock single-flight re-seal** — snapshots under `s.mu` then encrypts + writes the snapshot *without* holding the lock, so a slow USB write never blocks reads/writes; monotonic generation guard + retry + `/v1/health` surfacing; race-tested. Schema + FTS5 + triggers; **single-instance lock** (flock / LockFileEx) |
| **M3 embeddings (D9/D17)** | local CPU `cybertron`/`bge-small-en-v1.5` (384-dim) + `HashEmbedder` fallback; dim pinning |
| **M4 ingest** | chunk → local embed → store; timestamp parsing; soft-cap warning; agent-supplied entities |
| **M5 retrieval** | keyword (FTS5/BM25) + semantic (vec0/cosine) + **temporal** arms; RRF (k=60); **recency rerank**; token budget |
| **M6 REST API (§0.0)** | health, retain, recall, **reflect**, banks CRUD, memories list/delete; loopback + `Host`-header guard; optional bearer token; copy-paste snippet. No MCP, no setup/unlock routes (server runs only unlocked) |
| **Reflect + knowledge hierarchy (agent-driven)** | Memory **types** (`world`/`experience`/`observation`/`mental_model`, agent-set on retain). `POST /…/reflect` does a broad multi-arm gather + larger budget + associative entity expansion, then **prioritizes the agent's stored syntheses** (`mental_model` > `observation` > raw facts) — mirroring hindsight's reflect hierarchy. mykeep gathers; the agent's LLM synthesizes and retains its conclusion as a `mental_model` so future reflects build on it (no LLM in mykeep, no auto-consolidation). |
| **Passphrase** | **No complexity policy** — the user owns their passphrase strength; only a non-empty check + a prominent **NO RECOVERY** warning (TTY + GUI). |
| **Forgetting / pruning** | The agent supersedes stale syntheses via `retain {…, "supersedes":[ids]}` (mykeep deletes them after inserting the replacement); **orphan entities are pruned** automatically on delete (+ `store.PruneOrphans`). Mirrors hindsight's graph-maintenance / fold-and-delete — but LLM-adjudicated semantic dedup + auto-consolidation stay the agent's job (they need an internal LLM, which mykeep has by design). |
| **Auto-retain (capture + distill)** | Fixes *silent under-retention* without adding reasoning to mykeep: `POST /…/capture` + `mykeep capture` log each raw turn as a low-tier `experience` tagged `capture`, with **mechanical (no-LLM) dedup** (cosine ≥0.97 via `EmbedQuery`+`VectorSearch`). Captures are **excluded from recall/reflect by default** (`include_captures` opts in; `?type=&tag=` lists them). Captures are **not inserted into `vec_idx`**, so default recall stays on the fast vec0 KNN path (no tag anti-join → no brute-force); `include_captures` uses `VectorSearchExact` (brute-force over `embedding`) to reach them. A host hook makes the *trigger* automatic; the agent **distills** captures → `mental_model` via the existing `retain{supersedes}` (folds + deletes raw rows). Recipes in `integrations/claude-code/` (`UserPromptSubmit` capture + `Stop` distill nudge). Judgment stays the agent's. |
| **Agent guide** | `GET /v1/guide` + `mykeep guide` + a GUI "Copy full instructions" button serve the host LLM's **operating manual** (the full retain/recall/reflect/supersede protocol + memory types) — mykeep's equivalent of hindsight's ~40 KB reflect-agent system prompt. Required because the agent does all reasoning. |
| **GUI (cross-platform)** | `internal/gui` + `internal/app`: local web UI (embedded HTML, `go:embed`) opened in the default browser (pure Go via `os/exec`, no GUI toolkit/CGo). Lock screen → unlock → dashboard (status, agent snippet, remember, search). Default launch (no args); `serve` is the terminal equivalent |
| **M7 CLI** | `gui` (default) / `serve` / `snippet` / `guide` / `doctor` / `capture` / `retain` / `recall` / `memories` / `banks` / `version` |
| **M8 hardening** | graceful-shutdown flush + key zeroize; single-instance lock; persistence-failure surfaced in `/v1/health` |

**Tests:** ~123 tests across 10 packages (`go test ./...` green), plus a verified end-to-end
smoke (first-launch → retain → semantic recall → encrypted persistence across restart →
no plaintext on disk → wrong-password rejection → graceful shutdown). All adversarial-review
bugs fixed (AAD truncation, flush-error swallowing, recency rerank, applyPragmas leak,
embedder length guard, headless password floor).

## High-value items — DONE (2026-06-06, thoroughly tested)

- **`vec0` KNN as the default vector backend (D1)** — `internal/store/vec0.go` blank-imports
  `modernc.org/sqlite/vec`, probes at open, builds + backfills a `vec_idx` vtable, and routes
  `VectorSearch` through metadata-filtered KNN (bank+model); brute-force is the fallback for
  tag-filtered queries / when vec0 is absent. Both exact. Tests: vec0↔brute-force parity, tag
  routing, delete-sync. Still pure-Go / no-CGo / cross-compiles.
- **Temporal recall arm (D14)** — `internal/retrieval/temporal.go` parses a closed set (ISO dates,
  N days/weeks/months/years ago, yesterday/today/tomorrow, last/this/next week|month|year,
  Month YYYY, YYYY) and adds a third RRF arm over `event_at`; honors `query_timestamp`. Tests:
  table-driven parser + Monday-week + recall integration.
- **Migration framework** — `internal/store/migrate.go`: embedded numbered migrations, forward-only,
  per-migration transactions, `schema_version` + `min_binary`, **fail-closed** on a newer-than-code
  DB. Added `0002_graph.sql` (the `edge` table — schema is now graph-ready). Tests: fresh→latest,
  idempotent, fail-closed, real v1→v2 forward migration preserving data.
- **CLI breadth (M7)** — `doctor` (password-free diagnostics: sqlite/vec0/fts5, data dir, portability,
  setup, db size, single-instance lock) and thin HTTP-client subcommands `retain`/`recall`/
  `memories`/`banks`; `version` now reports sqlite + vec0. Verified end-to-end against a live server.

## Still deferred (lower priority)

1. **`PATCH /v1/settings`** with dim-pinning conflict handling (D16); apply `bank_id`
   validation uniformly across all bank routes.
2. **Key-in-RAM hardening.** `mlock`/`VirtualLock` the DEK; setup-time argon2 calibration;
   idle auto-lock + drive-removal detection (M8). *(Passphrase-entropy policy: dropped by design — the user owns their passphrase strength.)*
3. **DEK rotation** before approaching the GCM per-key seal budget (documentation-level today).
4. Minor: first launch loads the embedder twice; config model-id vs `embedding.model` cosmetic mismatch.

Update this file and `CLAUDE.md` as items land.
