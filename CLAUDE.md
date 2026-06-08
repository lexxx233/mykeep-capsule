# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

**v1 implemented; public repo, MIT-licensed** ([github.com/lexxx233/mykeep-memory-capsule](https://github.com/lexxx233/mykeep-memory-capsule)). The full design lives in `PLAN.md` (the source of truth for decisions D1–D20); done-vs-deferred status in `IMPLEMENTATION.md`. A working Go implementation is in place: encrypted store, local CPU embeddings, keyword + semantic + temporal recall, agent-driven reflect over a knowledge hierarchy, supersession + orphan pruning, and a REST API + cross-platform GUI. Runs end-to-end (first-launch setup → retain → recall/reflect → encrypted persistence across restarts).

**Build / test / run** (Go 1.26+, pure Go, no CGo):

```sh
go build ./cmd/mykeep          # or: make build  ->  bin/mykeep
go test ./...                   # or: make test
make guard                      # prove zero CGo in the dependency graph
make cross                      # cross-compile all six win/mac/linux × amd64/arm64 targets
./bin/mykeep serve             # first launch creates a password, then serves
go test ./internal/retrieval -run TestRRF -v   # a single test
```

> Note: this dev environment has Go at `$HOME/go-sdk/go` and uses `GOPATH=$HOME/go-work`; `/tmp/goenv` sets the needed env. `go test -race` needs `CGO_ENABLED=1`.

**Architecture (as built):** `cmd/mykeep` (CLI: gui[default]/serve/snippet/guide/doctor/capture/retain/recall/memories/banks/version) → `internal/gui` (cross-platform GUI: embedded local web app opened in the browser, pure-Go via os/exec) → `internal/app` (assembles the runtime from a password; shared by GUI + `serve`) → `internal/{paths,config,secret,setup}` (launch + password + KEK/DEK) → `internal/store` (in-RAM SQLite, whole-DB AES-256-GCM encryption via serialize/deserialize, debounced off-lock re-seal, FTS5 + vec0 KNN (brute-force fallback), single-instance lock) → `internal/{embed,vector}` (cybertron local CPU embeddings + hash fallback) → `internal/{ingest,retrieval}` (retain/recall + RRF + recency rerank) → `internal/server` (loopback REST API + copy-paste snippet). `internal/domain` holds the shared JSON types. The GUI starts locked and unlocks via a web password form (`/api/setup`, `/api/unlock`); `serve` unlocks via the TTY.

**CLI:** `gui` (default) / `serve` / `snippet` / `guide` / `doctor` / `capture` / `retain` / `recall` / `memories` / `banks` / `version`. The thin-client ops are HTTP clients of a running server; `doctor` is password-free diagnostics; `guide` prints the full agent operating manual (also `GET /v1/guide`).

**Auto-retain (capture + distill):** fixes silent under-retention with no LLM in mykeep. `POST /…/capture` (+ `mykeep capture`) logs each raw turn as a low-tier `experience` tagged `capture` with mechanical cosine dedup; captures are excluded from recall/reflect by default (`include_captures` / `?type=&tag=` opt-in). A host hook (`integrations/claude-code/`) makes the trigger automatic; the agent distills captures into `mental_model`s via the existing `retain{supersedes}`. Judgment stays the agent's.

**Forgetting:** the agent supersedes stale syntheses via `retain {supersedes:[ids]}` (mykeep deletes them); orphan entities auto-pruned on delete (`store.PruneOrphans`). LLM-adjudicated dedup/consolidation stays the agent's job. The host LLM needs the operating manual (`GuideText`) since mykeep does no reasoning.

**Implemented:** whole-DB encryption (D13) + debounced re-seal (D19, off-lock single-flight: snapshot under lock, encrypt+write off it so a slow USB write never blocks recall); local CPU embeddings (bge-small) + hash fallback; **vec0 KNN default** (modernc/sqlite/vec) with brute-force fallback for tag-filtered queries and include_captures (D1); keyword + semantic + **temporal** recall arms fused with RRF + recency rerank; **reflect** + knowledge hierarchy (memory `type`: world/experience/observation/mental_model; reflect does broad retrieval + entity expansion and prioritizes the agent's stored syntheses mental_model>observation>raw — mirrors hindsight's reflect hierarchy; mykeep gathers, the agent reasons + retains conclusions as mental_models); **migration framework** (versioned, fail-closed, `edge`/graph-ready schema); single-instance lock; cross-platform **GUI** + REST API; doctor + thin-client CLI. **No passphrase complexity policy** (user owns strength; NO-RECOVERY warning). ~123 tests, all 6 targets cross-compile CGO_ENABLED=0.

**Still deferred** (see IMPLEMENTATION.md): `PATCH /v1/settings` (D16), key-in-RAM hardening (mlock, argon2 calibration, entropy policy, idle auto-lock), DEK rotation. Update this file as those land.

## Vision

A **portable, USB-resident memory system for AI agents**: a single cross-platform Go binary that, together with its data files, lives entirely on a USB drive. The user plugs the drive into any machine (Windows, macOS, Linux), launches the executable, and has a local memory API + data store available to any AI agent or client on that host.

The design goal is *self-contained portability*: no installer, no host-side dependencies, no cloud round-trips for storage. Configuration and data both persist on the USB drive itself, so moving the stick to a new machine carries the full state with it.

## Inspiration: Hindsight

This project takes architectural cues from **Hindsight** (`~/Pr/hindsight`), an agent memory system by Vectorize.io. Skim `~/Pr/hindsight/README.md` and `~/Pr/hindsight/CLAUDE.md` before making non-trivial design decisions about the memory API or retrieval pipeline.

Key concepts we are borrowing:

- **Memory banks** as the top-level isolation unit (e.g., `bank_id="my-bank"`).
- **Retain / Recall** as the core API verbs — write a memory; query for relevant memories. (Hindsight also has *Reflect* for LLM-driven synthesis over stored memories; whether we ship Reflect in v1 is open.)
- **Multi-strategy retrieval**: semantic (vector) + keyword (BM25) + temporal filtering, fused with reciprocal rank fusion. Hindsight also does a graph-traversal pass; whether we replicate that depends on how much LLM-side extraction we do at write time.
- **LLM-assisted ingestion**: at `retain` time, an LLM extracts entities, relationships, and temporal anchors from raw content. This is what differentiates a memory system from a vector DB.

Key concepts we are **not** borrowing:

- Hindsight's Postgres-backed deployment (heavy, requires a separate process, OS-fussy on USB).
- Hindsight's Docker-first distribution model.
- A separate UI server on a second port (v1 is API-only; UI can come later).

## Locked technical decisions

- **Language/runtime**: Go. Single static binary, cross-compiled to win/mac/linux from one host. No runtime needed on the target machine.
- **SQLite driver**: `modernc.org/sqlite` (pure-Go, no CGo). This is what keeps the binary truly static and cross-platform; do not switch to `mattn/go-sqlite3` without an explicit reason, because CGo breaks the simple cross-compile story.
- **Storage**: SQLite, with three features stacked in a single database file on the USB drive:
  - Relational tables for memories, entities, relationships, and timestamps.
  - **FTS5** for BM25 keyword retrieval.
  - **`sqlite-vec`** extension for ANN vector retrieval.
  All four hindsight-style retrieval strategies (semantic, keyword, temporal, graph) can be served from this one engine without a second process.
- **Persistence root**: a `mykeep_kb/` directory *beside the executable on the USB drive*, not `$HOME` or any host-local path. This is what makes the system portable. Resolve paths relative to the binary's location, not the working directory.
- **Drive layout** (`make dist`): the six platform binaries (`mykeep-<os>-<arch>[.exe]`) and three launchers sit flat at the drive root; all data lives in `mykeep_kb/` beside them (created on first launch). No `bin/<os>-<arch>/` nesting — each binary resolves `mykeep_kb/` as its own sibling.

## First-launch setup flow

On the very first launch from a given USB drive, the server runs an interactive setup that captures and persists the user's LLM configuration to the USB drive itself:

- LLM provider (OpenAI, Anthropic, etc.)
- API key
- Base URL (for custom endpoints / proxies / local models)
- Default model

This config is written to a file alongside the binary on the USB drive and is loaded automatically on every subsequent launch. Users change it via a settings command/endpoint; otherwise it is sticky for the life of the drive.

Two non-obvious requirements:

1. **Secrets on a USB stick are physically portable** — if the drive is lost, the API key walks with it. The config store should encrypt the API key at rest with a key derived from a user-supplied passphrase, prompted on first launch.
2. **First-launch detection** is based on the presence of the config file in the binary's directory, not on host-side state. Plugging the drive into a fresh machine must not re-trigger setup.

## Open design questions

These are unresolved and worth flagging when you encounter them; do not silently commit to one path without checking with the user:

- Whether `Reflect` (LLM-driven synthesis over stored memories) ships in v1 or later.
- Whether write-time LLM extraction is mandatory or opt-in. Mandatory matches hindsight; opt-in lets the system function without a network connection and without an LLM key, at the cost of weaker recall quality.
- HTTP API shape: REST mirroring hindsight (`POST /retain`, `POST /recall`) vs. a more minimal verb set.
- Embedding model strategy: call out to the configured LLM provider's embedding endpoint, or bundle a small local embedding model with the binary (heavier binary, no network needed).
- Client SDKs: which languages get first-class SDKs. Hindsight ships Python + Node.js; we should at least match Node.js.

## When working in this repo

- The empty state is intentional — this is a greenfield project. Default behavior on the first non-trivial implementation step is to propose a layout to the user before writing code, not to scaffold silently.
- For any retrieval, ingestion, or memory-modeling decision, the hindsight repo at `~/Pr/hindsight` is the reference implementation. Read the relevant module there before designing ours.
- Keep the "runs from a USB stick on any OS" constraint top-of-mind. Any dependency that requires CGo, a system library, or a host-side install breaks the core premise.
