# joyvend.io — Implementation Plan (v1)

> **Status:** Design complete, pending your review. No code written yet.
> **Produced by:** a multi-agent research → synthesis → adversarial-review pass grounded in the
> hindsight reference (`~/Pr/hindsight`) and the current (2026) Go/SQLite ecosystem.
> **How to use this doc:** read §0 first (D1 is now decided by you; **D13 still needs your sign-off**;
> plus the corrections already folded in). Then skim the schematic (§1) and edit any **Open Decision**
> (§15) inline — those drive the milestones (§12). Items marked **†review** were added or fixed by
> the adversarial review pass. Everything is a near-final sketch: the first build task (M0) is to
> `go build` the skeleton and let the compiler/tests confirm the Go snippets.

---

## 0. Read this first — decisions, amendments, and corrections

### 0.0 ARCHITECTURE PIVOT (2026-06-06) — joyvend is an MCP memory server with NO LLM
**This supersedes all LLM/Chat/Ollama/remote-API/`reflect` content elsewhere in this doc** (§7, §9,
§11 config, milestones M3/M4 — to be propagated). The decision:

- joyvend is a **memory store + retrieval, exposed to AI agents as MCP tools** (`retain`/`recall` +
  bank admin). The **calling agent does ALL reasoning** (entity/fact extraction, synthesis) with its
  own model and passes structured results in; joyvend stores and retrieves. **Inversion of control.**
- **Removed entirely:** every Chat/LLM adapter (`internal/llm`), the LLM extraction step, the Ollama
  companion, the remote API, and **the API key**. MCP *sampling* was rejected (deprecated MCP
  2026-07-28, never well supported).
- **`reflect` — IMPLEMENTED as an agent-driven op with a knowledge hierarchy (update 2026-06-06):**
  not LLM synthesis inside joyvend, but a *gather* endpoint that mirrors hindsight's reflect
  hierarchy. Memories carry a **type** (`world`/`experience`/`observation`/`mental_model`, agent-set).
  Reflect does broad multi-arm retrieval + larger budget + associative entity expansion, then
  **prioritizes the agent's stored syntheses** (`mental_model` > `observation` > raw facts) so it
  builds on prior conclusions. joyvend assembles the context; the agent's LLM synthesizes and retains
  its conclusion as a `mental_model`. (No internal LLM, no auto-consolidation — those are hindsight's,
  which runs its own LLM + a background consolidation pipeline.)
- **Kept:** local pure-Go CPU **embeddings** (cybertron/bge-small — retrieval, not reasoning),
  SQLite + FTS5 + vec0, whole-DB encryption (debounced whole-blob re-seal, no journal — D19),
  retain/recall + banks.
- **Consequence:** with no API key, the master password's **only** job is encrypting the DB — one
  password, your memories, zero keys.
- **Interface: a pure local REST API on loopback — NO MCP, NO skill file.** Integration is a
  **copy-paste text snippet** that joyvend prints (startup banner + `joyvend snippet`) telling the
  user's AI client the endpoint + how to `retain`/`recall`; the agent (Claude Code/Cursor/any
  shell-or-fetch-capable client) calls the API with its existing tools. Default = loopback +
  Host-header guard, **session token OFF by default** (static tokenless snippet); `require_token` flag
  for local-process isolation (then the token is included in the snippet). (**D20**)

### 0.1 One decision is locked by you (D1); one still needs your sign-off (D13)

**✅ DECIDED — D1 (vector backend), per your call:** the **default vector backend is
`modernc.org/sqlite/vec`** — the pure-Go (ccgo-transpiled C, *not* cgo) subpackage of the modernc
driver that registers a real `sqlite-vec` `vec0` virtual table + KNN, storing vectors **inside the
same `joyvend.db` file**. This supersedes `CLAUDE.md`'s original `sqlite-vec`-C-extension decision
(impossible under a pure-Go driver) while keeping its intent: vectors in one SQLite file, one static
no-CGo cross-compilable binary. **Brute-force exact cosine over float32 BLOBs is retained as an
automatic fallback** (used if `vec0` isn't registered at runtime; also a correctness oracle) — it is
cheap insurance, not the primary path. Note both paths return the *same* exact top-K: `sqlite-vec`'s
`vec0` is itself an exact SIMD linear scan, so this is a speed/ergonomics choice, not an accuracy
one. **✅ VERIFIED by a running spike (2026-06-06)** — Go 1.26.4 installed; `modernc.org/sqlite@v1.52.0`
+ its `vec` subpackage fetched; a blank import auto-registered `vec0` (`vec_version() = v0.1.9`); a
`vec0(... distance_metric=cosine)` KNN query returned the same ordering as a Go brute-force cosine
scan; and the binary built clean under `CGO_ENABLED=0` ("statically linked, not a dynamic
executable"). Evidence in §5.1.1. The fallback remains as insurance, but the default is proven.

**✅ DECIDED — D13: encrypt the WHOLE DB at rest, per your call.** Not just the API key — *all* memory
content, the FTS5 index, entity names, and embedding vectors live inside the encryption boundary. The
`.db` is one AES-256-GCM blob on the stick (`joyvend.db.enc`); unlock decrypts it into an in-RAM
SQLite DB where FTS5 + vec0 run on plaintext; writes hit RAM and a **debounced flush** (D19) re-seals
the whole blob a few seconds later (sync on shutdown/eject); the password-derived KEK/DEK unlocks
everything. A stolen powered-off stick yields only ciphertext. **Cost:** the whole DB
sits in RAM while unlocked → a size ceiling, so a **soft warning fires at ~450 MB** (~240k memories at
384-dim). Full design + the write flow in §11.6; capacity in §16. (Two implementation sub-points
remain — re-seal cadence **D19**, and verifying modernc's serialize/deserialize — neither blocks the
decision.)

**🔲 NOTHING ELSE BLOCKS M0.** Remaining open items (D17 embedding-model tier, D18 local-LLM scope,
D19 re-seal cadence) all have working defaults in the plan; flip them in §15 if you disagree.

### 0.2 Corrections already folded in from the adversarial review (†review)

| Area | Was | Corrected to |
|---|---|---|
| `richlocal` offline embedder | claimed "pure-Go GoMLX, CGO_ENABLED=0" | **wrong** — Hugot's tokenizer ships a static archive needing `CGO_ENABLED=1`. Kept **out of v1** (D9); not in the no-CGo matrix; needs a pure-Go WordPiece tokenizer first. |
| Vector backend probe | `SELECT vec_version()` | **`CREATE VIRTUAL TABLE … USING vec0(...)`** probe selects the default `vec0` backend vs. the brute-force fallback — `modernc/vec` may not register `vec_version()`, so probing that symbol could misfire. |
| CGo-free guarantee | implicit | **explicit CI guard** (`CC=/bin/false go build` + a deps-grep test) proving the default build imports zero CGo. |
| Single-instance lock | "advisory lockfile" | **per-OS implementation specified**: `flock` (unix) / `LockFileEx` (Windows) — it is a syscall-divergent surface. |
| Cold vector scan perf (fallback path) | "mmap_size=256MB softens USB I/O" | **per-bank RAM cache is the primary mitigation** for the brute-force fallback; `mmap` is best-effort and may no-op on modernc's VFS / FAT/exFAT. First recall after replug is I/O-bound — documented. |
| Durability wording | "durable across yank" | **best-effort; safe-eject required** (USB/FAT lie about flush). |
| Canonical Go types | multiple fields under one JSON tag (uncompilable) | **rewritten with correct per-field tags** + a golden marshal test; frozen before M3. See §8.2. |
| argon2id params | `m=64MiB, t=3` | **calibrated at setup toward 256 MiB–1 GiB** (floor 256 MiB any host can meet) + a **passphrase-strength policy**. The rate-limit does nothing against an offline crack. |
| Key material in RAM | "zeroize best-effort" | + **`mlock`/`VirtualLock`** to keep key/passphrase out of swap; passphrase stays `[]byte` (never a Go `string`); `JOYVEND_PASSPHRASE` unset immediately after read. |
| GCM AAD | bind `schema_version` only | **bind the full secret-envelope header** (KDF algo+params+salt + enc algo+nonce + schema_version). |
| Loopback guard | bind 127.0.0.1 + unlock | + **validate the HTTP `Host` header** against loopback literals (DNS-rebinding), reject wildcard binds, session token from `crypto/rand` compared with `subtle.ConstantTimeCompare` + TTL. |
| Temporal arm | "regex heuristic" (hindsight uses `dateparser`) | **scoped to an enumerated, closed pattern set** for v1 (D14); unsupported phrasing → arm cleanly absent. |
| `HashEmbedder` | algorithm unspecified | **exact algorithm specified** + a real similarity test; decision on whether the offline semantic arm adds signal vs. duplicates BM25 in RRF (D15). |
| SQLite concurrency | unspecified | **single serialized writer (`MaxOpenConns`-bounded write conn) + RWMutex on the embedding cache**, with a `-race` concurrency test. |
| Migration testing | only fail-closed tested | + a **real N→N+1 migration fixture** proving data survives an upgrade. |
| Endianness test | round-trip through a mirror (always passes) | **assert fixed byte layout** (`float32(1.0)` → `00 00 80 3F`); commit **macOS + Windows CI jobs** (not "optional"). |

---

## 1. Schematic (for review/edit)

### 1.1 System topology — one binary set + one DB, all on the stick

```
   HOST (Windows / macOS / Linux)                 USB DRIVE :  <DRIVE>/joyvend/
 ┌───────────────────────────┐            ┌──────────────────────────────────────────────┐
 │  AI agent / app / SDK      │            │  joyvend.cmd  joyvend.command  joyvend.sh      │
 │  joyvend CLI (thin client) │            │      (thin OS+arch-detect launchers)           │
 └─────────────┬─────────────┘            │  bin/                                          │
               │ HTTP/JSON                 │    windows-amd64/joyvend.exe   darwin-arm64/…  │
               │ 127.0.0.1:8765            │    linux-amd64/joyvend         … (6 static     │
               │ (loopback only,           │                                  pure-Go bins) │
               ▼  Host-header checked)     │  data/   (created on first launch, SHARED)     │
 ┌───────────────────────────┐  reads/     │    ├── joyvend.config.json                     │
 │  joyvend  (running binary) │  resolves   │    │     plaintext: provider/model/base_url    │
 │  resolves data/ from its   │◀───────────│    │     + KDF params/salt + sealed secrets     │
 │  OWN location, never $HOME │  exe dir    │    └── joyvend.db                              │
 └───────────────────────────┘             │          SQLite: relational + FTS5 + vectors   │
                                            └──────────────────────────────────────────────┘
   No installer · no host service · no cloud for storage · state travels with the stick
```

### 1.2 Internal component stack (single process)

```
        ┌──────────────────────────── cmd/joyvend (CLI dispatch) ───────────────────────────┐
        │  serve · setup · unlock · settings · version · doctor · retain · recall · banks    │
        └───────────────┬──────────────────────────────────────────────┬────────────────────┘
                        │                                               │ (CLI memory ops are
                        ▼                                               ▼  thin HTTP clients)
        ┌──────────────────────── server (net/http, /v1) ───────────────────────────────────┐
        │  loopback+Host guard → setup/unlock gate (409/423/401) → session-token → handlers  │
        └───────┬───────────────────────┬───────────────────────────┬───────────────────────┘
                ▼                        ▼                           ▼
        ┌──────────────┐        ┌────────────────┐          ┌─────────────────┐
        │  ingest      │        │  retrieval     │          │ config / secret │
        │ (retain)     │        │ (recall)       │          │ /setup/paths    │
        │ chunk→       │        │ arms→RRF k=60→ │          │ argon2id+GCM,   │
        │ extract|raw→ │        │ rerank+recency→│          │ KEK/DEK,        │
        │ embed→store  │        │ token budget   │          │ exe-dir resolve │
        └──┬────┬──────┘        └──┬─────┬───────┘          └─────────────────┘
           │    │                  │     │
           ▼    ▼                  ▼     ▼
        ┌──────────┐  ┌──────────┐  ┌──────────────────────────────────────────────┐
        │ llm.Chat │  │embed.Emb.│  │ store (modernc.org/sqlite, pure-Go, no CGo)    │
        │ OpenAI/  │  │ OpenAI/  │  │ migrations · PRAGMAs · single-writer · DAOs    │
        │ Anthropic│  │ compat/  │  │ memory · memory_fts (FTS5) · embedding (BLOB)  │
        │ /compat/ │  │ Ollama/  │  │ vector: vec0 KNN; brute-force cosine fallback  │
        │ Ollama/  │  │ Hash     │  └──────────────────────────────────────────────┘
        │ None     │  └──────────┘
        └──────────┘
```

### 1.3 Retain flow

```
POST /v1/banks/{bank}/retain {items:[{content,tags,timestamp,metadata,...}]}
  └─ per item:
       chunk(content)                      RecursiveCharacter, 3000 chars, no overlap
         └─ per chunk:
              DEFAULT (local, no LLM):                            OPTIONAL (Chat key set):
                 unit = raw chunk verbatim                           Chat.Complete(extraction schema)
                 (enriched=0, type=experience,                         → facts[{text,entities,...}]
                  event_at=timestamp|now)                              (enriched=1)
              embed unit text:  LocalEmbedder (cybertron, 384-dim, CPU)  [hash fallback if model absent]
              ── one transaction ──
                 INSERT memory (+trigger → memory_fts)
                 INSERT embedding BLOB (model, dim, L2-normalized)
                 UPSERT entity / memory_entity / edge   (only when extracted)
  └─ RetainResponse{success, items_count, usage*}   usage non-nil only when the LLM ran
```

### 1.4 Recall flow

```
POST /v1/banks/{bank}/recall {query, tags, tags_match, max_tokens, query_timestamp?, trace?}
   tokenize(query)
   ── arms run in parallel, each ranked, each may be absent ──
     KEYWORD  (always, if word tokens)  FTS5 bm25(), bank/tag-filtered
     SEMANTIC (if an embedder exists)   EmbedQuery → vec0 KNN (default), filtered by
                                        (bank_id, ACTIVE model); brute-force cosine over
                                        RAM-cached vectors as fallback if vec0 absent
     TEMPORAL (if a date window parses) units overlapping window, recency|cosine ordered
   RRF fuse (k=60, score += 1/(k+rank), rank from 1)  →  cap 300 candidates
   rerank-lite:  base = 1 − 0.9·i/(n−1)          (RRF rank → [0.1,1.0])
                 weight = base · (1 + 0.2·(recency−0.5))   (linear 365d decay; 0.5 if no event_at)
   take top k_final·2  →  greedy fill until Σ tokens(chars/4) > max_tokens  (BREAK on overflow)
   → RecallResponse{results:[…] ordered best-first, NO score in body; scores in trace iff trace}
```

---

## 2. Overview

### 2.1 Core premise
A single pure-Go, no-CGo, statically-linked binary plus one SQLite file that live entirely in the
binary's own directory on a USB drive. Plug the stick into any Windows/macOS/Linux host, run the
binary, and a local-loopback memory API (**retain** / **recall**) is available to any AI agent on
that host. No installer, no host-side dependency, no cloud round-trip for storage. Config and data
both persist on the stick, so moving the stick carries full state.

### 2.2 Goals (v1)
- **Retain + Recall** as the two shipping verbs, faithful to hindsight's payload shapes (banks,
  `tags`, `tags_match`, token-budget trimming, ordered-results-as-contract).
- **Multi-strategy recall:** keyword (FTS5/BM25) + semantic (vector) + temporal, fused with
  Reciprocal Rank Fusion (k=60, verified against hindsight `fusion.py`). No cross-encoder in v1
  (RRF-passthrough order + recency boost — also a verified hindsight mode).
- **Graceful offline degradation is mandatory:** with no LLM key and no network, retain stores raw
  chunks (no extraction), recall runs keyword + temporal (+ a degraded local-fallback vector arm),
  and reflect is cleanly disabled.
- **Encrypted secret at rest:** API key (and any other sensitive string — a **master-password
  → encrypted keyring**) sealed with AES-256-GCM under an argon2id-derived key. First-launch
  detection = presence of the config file beside the binary.
- **True portability:** all six platform binaries on the stick behind thin launchers; data resolved
  from the binary location, never `$HOME`/cwd; tolerant of exFAT/FAT32 and surprise removal.

### 2.3 Non-goals (v1 — reserved for later)
- **Reflect** (LLM synthesis / agentic tool loop) — route reserved, returns `501`.
- **Graph-traversal recall arm** — schema is graph-ready (`entity`, `edge`) but the arm is deferred
  to v2; RRF degrades to the arms present.
- Cross-encoder rerank, observation/consolidation, mental models, directives, webhooks, audit logs,
  async/operations API, `budget` tiers, `chunks`/`source_facts` in results, compound `tag_groups`,
  multi-tenant `/default/` segment, second UI port, Postgres, Docker-first distribution.

### 2.4 Success criteria (corrected †review)
1. `CGO_ENABLED=0 go build` cross-compiles to all six targets from one host; a CI guard proves the
   default build imports **zero** CGo.
2. Fresh stick → `joyvend serve` runs setup, prompts a passphrase, writes an encrypted config;
   replug into a second machine → setup is **not** re-triggered, only an unlock prompt.
3. Default (local, no LLM, no network): retain stores a raw chunk + a **local** 384-dim embedding,
   recall fuses keyword + semantic + temporal via RRF — **no outbound network at all**, no hang.
4. Optional online (Chat key set): retain additionally extracts facts/entities; recall unchanged;
   results trimmed to `max_tokens`.
5. Wrong passphrase fails loudly (`401`) via GCM auth failure. **A stolen powered-off stick yields
   only ciphertext for *everything* — content, FTS index, and vectors, not just the API key (D13).**
6. The same SQLite file round-trips a stick between an online and an offline host without
   dimension/schema corruption; an N→N+1 binary upgrade preserves existing data.

---

## 3. Architecture

joyvend is one process exposing a local HTTP API and a thin CLI over the same core.

- **paths** — the portability keystone. Resolves the data dir from `os.Executable()` →
  `filepath.EvalSymlinks` → `filepath.Dir`, then `joyvend_kb/` beside the binary (the binaries sit
  flat at the drive root, so all six share one `joyvend_kb/`). Detects go-run/temp-exe,
  macOS AppTranslocation, and read-only mounts (temp-write probe); on failure falls back to
  `os.UserConfigDir()/joyvend` with `portable=false`. Re-resolves at every startup (Windows drive
  letters churn). Exposes `DataDir()`, `DBPath()`, `ConfigPath()`, `IsFirstLaunch()`, `Portable()`.
- **config** — loads/saves `joyvend.config.json` (atomic temp+rename, `fsync` temp + dir). Holds
  plaintext provider/model/base-url/embedding block + the sealed-secret envelope. Never serializes a
  secret in cleartext (`json:"-"`).
- **secret** — argon2id KDF + AES-256-GCM seal/open + a **KEK/DEK** split (passphrase→KEK wraps a
  random DEK; secrets encrypted under the DEK so a passphrase change only re-wraps the DEK).
  `KeyStore` holds decrypted material in `mlock`'d memory behind a mutex; zeroized on lock/shutdown.
- **setup** — interactive first-launch TTY flow; also the engine behind `POST /v1/setup`. A shared
  validator (used by both paths) enforces the anthropic-needs-a-separate-embedder rule.
- **store** — SQLite open (`modernc.org/sqlite`), PRAGMA tuning, `go:embed` forward-only migrations
  gated by `schema_version`, per-OS single-instance lock, DAOs. A `Store` interface lets the opt-in
  `vec` build swap the driver behind a build tag without touching call sites. **Single serialized
  writer**; reads concurrent.
- **vector** — embedding storage + search. Default: `vec0` KNN via the pure-Go
  `modernc.org/sqlite/vec` subpackage, pre-filtered by `(bank_id, model)` and tags. Fallback (if
  `vec0` isn't registered): brute-force exact cosine over the same unit-normalized float32 BLOBs,
  RAM-cached per `(bank, model)`. A startup `vec0`-vtable probe selects which. `encoding/binary`
  little-endian; the `embedding.vec` BLOB feeds both paths.
- **embed** — `Embedder` interface. **`LocalEmbedder`** (cybertron/spaGO, pure-Go, CPU,
  `bge-small-en-v1.5` 384-dim, model bundled in `data/`); `HashEmbedder` last-resort fallback. **No
  remote, no LLM** — embeddings are the only model joyvend runs, and it's local.
- **server** *(the only interface, §0.0)* — pure-Go `net/http` REST API on loopback; the calling
  agent uses its shell/fetch tool to hit it. Prints a copy-paste integration snippet (also via
  `joyvend snippet`). **No MCP, no `internal/llm`** — the agent is the LLM and does all reasoning.
- **ingest** — the retain pipeline (chunk → local embed → store; agent may supply structured entities).
- **retrieval** — the recall pipeline.
- **domain** — shared Go types (the frozen JSON/SDK contract).
- **server** — `net/http` router, gating + loopback/Host-header middleware, handlers, and the thin
  CLI HTTP client.

---

## 4. Repository layout

Module path `joyvend.io` (an app, everything under `internal/`). Pin `modernc.org/sqlite v1.52.0`.

```
joyvend.io/
├── go.mod                       # module joyvend.io; pin modernc.org/sqlite v1.52.0
├── go.sum
├── CLAUDE.md
├── PLAN.md                      # this file
├── Makefile                     # cross-compile matrix + dist drive layout
├── README.md                    # USB usage, exFAT, Gatekeeper/SmartScreen, safe-eject
├── SECURITY.md                  # threat model + what is/ isn't encrypted (D13)
├── .github/workflows/ci.yml     # 6-target CGO_ENABLED=0 matrix + no-CGo guard + mac/win jobs
├── cmd/joyvend/main.go          # arg parse → resolve paths → first-launch detect → dispatch
└── internal/
    ├── paths/        paths.go paths_test.go
    ├── config/       config.go config_test.go
    ├── secret/       secret.go secret_test.go            # argon2id + GCM + KEK/DEK + KeyStore
    ├── setup/        setup.go                            # TTY flow + POST /v1/setup engine
    ├── store/
    │   ├── store.go migrate.go memories.go entities.go fts.go banks.go lock_unix.go lock_windows.go
    │   ├── cryptdb.go                            # D13/D19: encrypted blob ↔ in-RAM DB, debounced re-seal
    │   ├── migrations/0001_init.sql
    │   └── store_test.go
    ├── vector/       vec0.go bruteforce.go encode.go vector_test.go    # vec0 default; bruteforce fallback
    ├── embed/        embed.go local.go hash.go openai.go compat.go ollama.go embed_test.go  # local.go = cybertron
    ├── llm/          chat.go none.go openai.go anthropic.go compat.go ollama.go llm_test.go  # optional/online
    ├── ingest/       chunk.go extract.go ingest.go ingest_test.go
    ├── retrieval/    recall.go fusion.go temporal.go recall_test.go
    ├── domain/       types.go types_test.go              # frozen contract + golden marshal test
    └── server/       server.go handlers.go client.go server_test.go
```

`vec0.go` carries `//go:build !novec`; `lock_unix.go`/`lock_windows.go` use per-OS build constraints.
`embed/local.go` wraps cybertron/spaGO (pure-Go, the default embedder); the bundled model lives in
`data/models/`, not in the repo.

---

## 5. Storage & retrieval (resolved/amended)

### 5.1 Driver & vector backend — DECIDED (D1)
- **Driver: `modernc.org/sqlite` v1.52.0 (pure Go, no CGo).** FTS5 and JSON1 are compiled into the
  default amalgamation (independently verified: `Fts5*` symbols present). The keyword leg needs no
  build tag — but M0 includes a `CREATE VIRTUAL TABLE … USING fts5(x)` smoke test, because an
  FTS5-absent build only fails at query time with `no such module: fts5`.
- **Default vector backend: `modernc.org/sqlite/vec`** — the pure-Go (ccgo-transpiled C, *not* cgo)
  subpackage of the same driver, blank-imported to register a real `sqlite-vec` `vec0` virtual table
  + KNN. Vectors live as `vec0` rows **inside the same `joyvend.db` file**, no second store, no CGo,
  cross-compiles to all six targets exactly like the parent driver. This supersedes `CLAUDE.md`'s
  original `sqlite-vec`-C-extension decision, which is literally impossible under a pure-Go driver.
- **Important — same recall profile as brute force:** `sqlite-vec`'s `vec0` KNN is itself an
  **exact, SIMD-accelerated linear scan** in current versions (not a sub-linear graph ANN), so the
  default and the fallback return the *same* exact top-K. `vec0` is a constant-factor speedup that
  runs inside the SQL engine — not a different accuracy profile. Genuine sub-linear ANN, if ever
  needed, comes from a future HNSW or a newer `sqlite-vec` index mode, not from this switch.
- **Automatic fallback: brute-force exact cosine in Go over float32 BLOBs.** Retained, not deleted —
  it is cheap insurance and a correctness oracle. At startup we probe by attempting
  `CREATE VIRTUAL TABLE _probe USING vec0(e float[2])`; **if it errors with `no such module: vec0`**
  (subpackage missing/unregistered at the pinned version) we fall back to the Go scan, and the
  `embedding` BLOB column feeds *both* paths identically. So a broken/absent `vec` subpackage
  degrades performance, never correctness or availability.
- **✅ Verified by spike (2026-06-06) — see §5.1.1.** The "M0 gate" is satisfied; the spike is kept
  as a regression test (`internal/vector`), not a blocker.
- **Alternative driver (escape hatch, still no-CGo): `-tags vec`** selects `ncruces/go-sqlite3` +
  `asg017/sqlite-vec-go-bindings/ncruces` (genuine upstream `sqlite-vec` via WASM/wazero) for the
  rare case `modernc/vec` proves buggy or slower than upstream. CGo + `mattn` is explicitly **not**
  shipped.

### 5.1.1 D1 verification spike — evidence (2026-06-06)
Installed Go 1.26.4 (linux/amd64) and ran a standalone spike:
- `go get modernc.org/sqlite@v1.52.0` — the `vec` subpackage **is present** at the pinned version;
  `go list modernc.org/sqlite/vec` resolves. It is pure-Go (ccgo-transpiled; signatures take
  `*libc.TLS`, modernc's pure-Go libc — no `import "C"`).
- Registration is automatic: `vec/patches.go`'s `init()` calls `sqlite3_auto_extension(Xsqlite3_vec_init)`,
  so a **blank import** `import _ "modernc.org/sqlite/vec"` registers `vec0` on every connection — no
  per-connection hook needed.
- `SELECT vec_version()` → **`v0.1.9`** (extension live).
- `CREATE VIRTUAL TABLE v USING vec0(embedding float[4] distance_metric=cosine)` succeeded; inserted
  5 vectors as JSON arrays; KNN `… WHERE embedding MATCH ? AND k = 4 ORDER BY distance` returned the
  top-4 in order **[1, 2, 3, 4]**.
- **Parity with the fallback:** a Go brute-force cosine scan returned the identical order, and vec0's
  cosine *distance* equals `1 −` the Go cosine *similarity* for every row (e.g. 0.4000 ↔ 0.6000).
  Magnitude-invariance confirmed: `doc3=[3,4,0,0]` (‖v‖=5) scored 0.6, same as its unit direction.
- **No-CGo proof:** `CGO_ENABLED=0 go build` produced an ELF reported by `file` as
  *"statically linked … not a dynamic executable."*
- Spike location: `/tmp/vecspike/` (`main.go` is the seed for the `internal/vector` regression test).

### 5.2 FTS5 wiring
External-content FTS5 over `memory` (`content='memory'`, `content_rowid='id'`) so text is stored
once. Sync triggers keep it consistent. Keyword query:
`SELECT rowid, bm25(memory_fts) AS score FROM memory_fts WHERE memory_fts MATCH ? ORDER BY score`
joined to `memory` for bank/tag filtering. Skip the arm if the query has no word tokens.

### 5.3 Vector storage + the two scan paths (corrected †review)
Unit-normalized float32 packed little-endian into the `embedding.vec` BLOB (`dim*4` bytes), `norm=1.0`
stored so cosine reduces to a dot product. Decode with `encoding/binary` (no `unsafe`). `dim` +
`model` stored per row so multiple models coexist. **The same BLOB column is the source of truth for
both paths** — the `vec0` virtual table mirrors it, and the Go fallback scans it directly.
- **`vec0` (default):** insert each embedding into a per-bank `vec0` virtual table; recall issues a
  KNN `MATCH` pre-filtered by bank + active `model`. `vec0` does the exact scan in transpiled-C.
- **Brute-force (fallback):** the Go scan's `WHERE` MUST filter by the active query `model`
  (not just bank/tags), or it cosine-compares across incompatible spaces (e.g. remote vs. `hash`).
  `idx_embedding_bank_model` backs this.
- **Per-`(bank_id, model)` RAM cache** of a contiguous `[]float32` is the **primary** cold-I/O
  mitigation **on the fallback path** (mmap is best-effort). Cache is `RWMutex`-guarded and
  **invalidated on retain/delete/bank-delete**; bounded by a configurable max-RAM ceiling with LRU
  eviction per `(bank, model)`. (`vec0` manages its own page access; the cache is fallback-only.)

### 5.4 Recall algorithm (v1)
```
recall(bank, query, max_tokens, tags, tags_match):
  arms = []
  toks = tokenize(query)
  if toks:  arms += [ rank(FTS5_bm25(bank, query, tags, limit=L)) ]                 # KEYWORD (always)
  if embedder_available:
      qv = embedder.EmbedQuery(query)
      if vec0_available: sem = vec0_knn(bank, model, qv, tags, limit=L)
      else:              sem = bruteforce_cosine(bank, model, qv, tags, limit=L)    # model-scoped, cached
      arms += [ rank(sem) ]                                                          # SEMANTIC (if embedder)
  win = extractTemporalWindow(query, query_timestamp|now)        # closed pattern set, D14
  if win: arms += [ rank(units_overlapping(bank, win, tags).order(recency|cosine).limit(L)) ]  # TEMPORAL

  merged = {}                                                    # RRF, k=60 (verified vs hindsight)
  for arm in arms:
    for rank, doc in enumerate(arm, start=1): merged[doc.id] += 1.0/(60+rank)
  cands = sort_desc(merged)[:300]                                # cap_per_source parity

  n = len(cands)                                                 # RRF-passthrough rerank + recency
  for i,c in enumerate(cands):
    base = 1.0 - 0.9*i/max(1,n-1)
    rec  = recency_score(c.event_at, now)                        # linear 365d decay → [0.1,1.0]; 0.5 if none
    c.w  = base * (1 + 0.2*(rec-0.5))
  cands = sort_desc_by(cands, .w)

  # k_final formula (corrected — was circular †review):
  k_final = clamp(max_tokens / AVG_FACT_TOKENS, 5, 100)          # AVG_FACT_TOKENS = 64 (tunable const)
  out, used = [], 0                                              # greedy budget; BREAK on overflow
  for c in cands[: k_final*2]:
    t = count_tokens(c.text)                                     # v1: chars/4 (D12)
    if used+t > max_tokens: break
    out += [c]; used += t
  return out  # NO score in body; per-arm ranks + rrf_score + weight into trace iff trace=true
```

### 5.5 RRF
Exactly hindsight's `reciprocal_rank_fusion` (verified in `engine/search/fusion.py`):
`score(d) += 1/(k+rank)`, rank from 1, **k=60** default. Parameter-free across incomparable
cosine/BM25/date signals. Well-defined with one or two arms — which is what makes offline
degradation clean.

### 5.6 Modes & degradation matrix
**Default v1 is the middle column (fully local, no LLM).** Left column is the optional online add-on;
right column is the rare model-missing fallback.
| Capability | + Chat key (optional, online) | **DEFAULT: local, no LLM** | Model file missing (fallback) |
|---|---|---|---|
| Ingestion | LLM extracts facts+entities+temporal | deterministic chunk → store verbatim | deterministic chunk → store verbatim |
| Embeddings | LocalEmbedder (or remote if configured) | **LocalEmbedder** (cybertron, 384-dim, CPU) | `HashEmbedder` (lexical) |
| Recall arms | keyword + semantic + temporal | keyword + semantic + temporal | keyword + temporal (semantic dropped) |
| Rerank | RRF-passthrough + recency | same | same |
| Graph arm | populated (v2 arm) | n/a (no extraction) | n/a |
| Reflect | available (v2) | disabled (`ErrLLMUnavailable`) | disabled |

> **D15:** when the `HashEmbedder` fallback is active its semantic arm largely duplicates BM25, so the
> semantic arm is dropped in that mode to avoid double-counting in RRF. With the default local model
> the semantic arm carries real signal and is kept. See §15.

### 5.7 SQLite concurrency model (†review)
The HTTP server is multi-goroutine; the file is single-writer under DELETE journal.
- One `*sql.DB` for **writes** with `SetMaxOpenConns(1)` (serialized writer) **or** an explicit
  write mutex; a separate read path. Reads tolerate `busy_timeout=5000`.
- The per-`(bank,model)` embedding cache is `RWMutex`-guarded.
- M2 ships a `go test -race` concurrency test: N goroutines retaining + recalling one bank, asserting
  no FTS/embedding corruption and no data race.

### 5.8 PRAGMAs (every connection)
`journal_mode` = detect-and-fallback: attempt WAL only on known-good local FS; **default DELETE on
removable media** (single-file, no `-wal`/`-shm` sidecars, survives surprise removal and cross-host
replug). `synchronous=FULL` (best-effort durability — *not* a guarantee on USB; safe-eject is the
real protection), `busy_timeout=5000`, `foreign_keys=ON`, `temp_store=MEMORY`, `cache_size=-65536`
(~64 MB), `mmap_size=268435456` (256 MB, **best-effort**). On clean shutdown/SIGINT: `PRAGMA
optimize` then close.

---

## 6. Data model — SQLite schema

`internal/store/migrations/0001_init.sql`:

```sql
PRAGMA foreign_keys = ON;

CREATE TABLE schema_version (
  version    INTEGER NOT NULL,            -- highest migration applied
  min_binary TEXT    NOT NULL DEFAULT '', -- minimum joyvend version this drive requires
  updated_at INTEGER NOT NULL
);

CREATE TABLE bank (
  bank_id    TEXT PRIMARY KEY,
  name       TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- One memory unit = one extracted fact (online) OR one raw chunk (offline).
CREATE TABLE memory (
  id          INTEGER PRIMARY KEY,
  bank_id     TEXT    NOT NULL REFERENCES bank(bank_id) ON DELETE CASCADE,
  content     TEXT    NOT NULL,
  fact_type   TEXT    NOT NULL DEFAULT 'experience', -- world|experience (observation reserved, v2)
  context     TEXT,
  document_id TEXT,
  created_at  INTEGER NOT NULL,           -- ingestion time
  event_at    INTEGER,                    -- temporal anchor; NULL = timeless
  event_end   INTEGER,                    -- range end; NULL unless a range
  metadata    TEXT,                       -- JSON1, opaque map[string]string round-trip
  embedder    TEXT,                       -- embedder id + quality tier (NULL if not embedded)
  enriched    INTEGER NOT NULL DEFAULT 0  -- 1 if LLM-extracted; 0 if raw chunk
);
CREATE INDEX idx_memory_bank_time ON memory(bank_id, event_at, created_at);

CREATE TABLE memory_tag (
  memory_id INTEGER NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
  tag       TEXT    NOT NULL,
  PRIMARY KEY (memory_id, tag)
);
CREATE INDEX idx_memory_tag_tag ON memory_tag(tag);

CREATE TABLE embedding (
  memory_id INTEGER NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
  bank_id   TEXT    NOT NULL,
  model     TEXT    NOT NULL,             -- "text-embedding-3-small" | "hash" | "minilm"
  dim       INTEGER NOT NULL,
  vec       BLOB    NOT NULL,             -- dim*4 bytes, little-endian float32, L2-normalized
  norm      REAL    NOT NULL DEFAULT 1.0,
  PRIMARY KEY (memory_id, model)
);
CREATE INDEX idx_embedding_bank_model ON embedding(bank_id, model);

CREATE TABLE entity (
  id      INTEGER PRIMARY KEY,
  bank_id TEXT NOT NULL REFERENCES bank(bank_id) ON DELETE CASCADE,
  name    TEXT NOT NULL,
  type    TEXT,
  UNIQUE (bank_id, name, type)
);
CREATE TABLE memory_entity (
  memory_id INTEGER NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
  entity_id INTEGER NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  PRIMARY KEY (memory_id, entity_id)
);
CREATE TABLE edge (
  bank_id   TEXT    NOT NULL,
  src       INTEGER NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  dst       INTEGER NOT NULL REFERENCES entity(id) ON DELETE CASCADE,
  relation  TEXT    NOT NULL,
  memory_id INTEGER REFERENCES memory(id) ON DELETE CASCADE
);
CREATE INDEX idx_edge_src ON edge(bank_id, src);

CREATE VIRTUAL TABLE memory_fts USING fts5(
  content, content='memory', content_rowid='id', tokenize='porter unicode61'
);
CREATE TRIGGER memory_ai AFTER INSERT ON memory BEGIN
  INSERT INTO memory_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER memory_ad AFTER DELETE ON memory BEGIN
  INSERT INTO memory_fts(memory_fts, rowid, content) VALUES('delete', old.id, old.content);
END;
CREATE TRIGGER memory_au AFTER UPDATE ON memory BEGIN
  INSERT INTO memory_fts(memory_fts, rowid, content) VALUES('delete', old.id, old.content);
  INSERT INTO memory_fts(rowid, content) VALUES (new.id, new.content);
END;
```

By default a `vec0` virtual table (from `modernc.org/sqlite/vec`) mirrors `embedding.vec` per bank
and serves KNN; the `embedding` table remains the source of truth and feeds the brute-force fallback
when `vec0` is unavailable. **If content encryption (D13) is adopted, the FTS5 index, the `vec0`
table, and the embedding BLOBs are all content-derived and MUST sit inside the encryption boundary**
(whole-DB encryption handles this automatically; per-column does not).

---

## 7. HTTP API

All endpoints under `/v1`. Server binds `127.0.0.1:8765` by default; `Content-Type: application/json`.

### 7.1 Lifecycle (non-bank)
**Setup + unlock are startup/CLI steps, not HTTP routes (§11.1).** The server is only ever up when
already set-up and unlocked, so there are **no `/setup`, `/unlock`, `/lock` endpoints** — when the
HTTP server is listening, every route below is live.

| Method | Path | Request | Response | Notes |
|---|---|---|---|---|
| `GET` | `/v1/health` | — | `HealthResponse` | Always 200. `portable=false` warns config landed on host; `content_encrypted=true`. |
| `GET` | `/v1/settings` | — | `Settings` (no secrets) | |
| `PATCH` | `/v1/settings` | partial | `Settings` | Change embedding model / flags; dim conflict → `400` (D16). No api_key (none exists). |

### 7.2 Banks
| Method | Path | Response |
|---|---|---|
| `GET` | `/v1/banks` | `{banks:[BankSummary]}` |
| `PUT` | `/v1/banks/{bank_id}` | `Bank` (upsert + create) |
| `DELETE` | `/v1/banks/{bank_id}` | `{deleted:true, bank_id}` (cascades) |

Banks auto-create lazily on first `retain`/`recall`. `bank_id` ~ `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`.

### 7.3 Memory verbs
| Method | Path | Req → Resp | Notes |
|---|---|---|---|
| `POST` | `/v1/banks/{bank_id}/retain` | `RetainRequest` → `RetainResponse` | v1 always synchronous; `async` accepted/ignored. |
| `POST` | `/v1/banks/{bank_id}/recall` | `RecallRequest` → `RecallResponse` | Ordered `results`, trimmed to `max_tokens` (default 4096). |
| `GET` | `/v1/banks/{bank_id}/memories?type=&q=&tags=&limit=100&offset=0` | → `ListMemoriesResponse` | Admin browse; offset/limit pagination lives here. |
| `GET` | `/v1/banks/{bank_id}/memories/{memory_id}` | → `Memory` | `404` if absent. |
| `DELETE` | `/v1/banks/{bank_id}/memories/{memory_id}` | → `{deleted:true}` | |
| `POST` | `/v1/banks/{bank_id}/reflect` | → `501` | Reserved for v2. |

### 7.4 Loopback & CSRF (corrected †review)
*(No setup/locked gating — the server runs only when set-up + unlocked, §11.1.)*
- Validation → `400 {error,field,message}`; unknown bank/memory → `404`.
- **Loopback guard validates BOTH the socket address AND the `Host` header** against loopback
  literals (`127.0.0.0/8`, `::1`) to stop DNS-rebinding from a browser. Wildcard binds rejected
  unless an explicitly-named config flag is set (logged loudly).
- **Bearer token on bank routes — OFF by default** (D20: keeps the copy-paste snippet tokenless).
  Enable with `require_token`: a `crypto/rand` token generated at unlock, **printed in the integration
  snippet**, compared with `subtle.ConstantTimeCompare`. An `Origin`/`Referer` check (or a required
  custom header) blunts browser CSRF regardless.

### 7.5 Examples
```
POST /v1/banks/my-bank/retain
{ "items":[ { "content":"Met Emily, my new roommate, at the apartment on 2026-05-01.",
              "tags":["user_a"], "metadata":{"source":"chat"},
              "timestamp":"2026-05-01T18:00:00Z" } ] }
→ 200 { "success":true, "bank_id":"my-bank", "items_count":1, "async":false,
        "usage":{"input_tokens":312,"output_tokens":88,"total_tokens":400} }   // usage null offline

POST /v1/banks/my-bank/recall
{ "query":"who is my roommate", "tags":["user_a"], "tags_match":"any", "max_tokens":4096 }
→ 200 { "results":[ { "id":"42", "text":"Emily is the user's roommate (since 2026-05-01).",
        "type":"experience", "entities":["Emily"], "tags":["user_a"],
        "occurred_start":"2026-05-01T18:00:00Z", "mentioned_at":"2026-05-01T18:00:00Z" } ] }
```
`entities` is populated only in online (extracted) mode; offline recall returns `entities: []`.

---

## 8. CLI & domain types

### 8.1 CLI surface
**Server / lifecycle:** `serve` (`--addr`; first launch runs setup on a TTY; on start prints the
copy-paste integration snippet), `setup` (refuses if config exists), `snippet` (re-prints the
paste-ready block for the user's AI client — endpoint + retain/recall examples, incl. the token iff
`require_token`), `unlock` (diagnostic), `settings [get|set <k> <v>]`, `version`
(version+SHA+SQLite version+`vec_available`), `doctor` (data dir + portability, FS type, journal
mode, embedder tier, schema_version, lock status, FTS5 smoke, **cold-scan benchmark**, stale
`*.tmp` cleanup).
**Memory ops (thin HTTP clients, default `127.0.0.1:8765`):**
`retain --bank --tag… --metadata k=v… --timestamp ISO (--content|--file|-)`,
`recall --bank --query [--tag…] [--tags-match …] [--max-tokens] [--trace] [--json]`,
`memories --bank [--type --q --tag… --limit --offset]`, `banks`, `bank delete <id>`.
**Conventions:** every server-touching command checks `GET /v1/health`, prompts + `/unlock` if
locked; `--server <url>` global; exit codes `0 ok / 1 user-config / 2 locked-unauthorized /
3 unreachable`; printed paths are absolute, resolved from the binary, never cwd.

### 8.2 Canonical domain types (corrected & frozen before M3 — †review)
`internal/domain/types.go` — each field has its own correct tag; a golden marshal test
(`types_test.go`) asserts the exact key set.

```go
// ---- Retain ----
type RetainRequest struct {
	Items []MemoryItem `json:"items"`           // REQUIRED, len>=1
	Async bool         `json:"async,omitempty"` // accepted, ignored (v1 always sync)
}
type MemoryItem struct {
	Content    string            `json:"content"`               // REQUIRED, non-empty
	Timestamp  *string           `json:"timestamp,omitempty"`   // ISO8601 | nil(now) | "unset"
	Context    *string           `json:"context,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	DocumentID *string           `json:"document_id,omitempty"`
	Entities   []EntityInput     `json:"entities,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
}
type EntityInput struct {
	Text string  `json:"text"`
	Type *string `json:"type,omitempty"`
}
type RetainResponse struct {
	Success    bool        `json:"success"`
	BankID     string      `json:"bank_id"`
	ItemsCount int         `json:"items_count"`
	Async      bool        `json:"async"`
	Usage      *TokenUsage `json:"usage,omitempty"` // nil when extraction off/offline
}
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ---- Recall ----
type RecallRequest struct {
	Query          string   `json:"query"`
	Types          []string `json:"types,omitempty"`           // default [world,experience]
	MaxTokens      int      `json:"max_tokens,omitempty"`      // default 4096
	Tags           []string `json:"tags,omitempty"`
	TagsMatch      string   `json:"tags_match,omitempty"`      // any|all|any_strict|all_strict (def any)
	QueryTimestamp *string  `json:"query_timestamp,omitempty"`
	Trace          bool     `json:"trace,omitempty"`
}
type RecallResponse struct {
	Results []RecallResult         `json:"results"`
	Trace   map[string]interface{} `json:"trace,omitempty"`
}
type RecallResult struct {
	ID            string            `json:"id"`
	Text          string            `json:"text"`
	Type          *string           `json:"type,omitempty"`
	Entities      []string          `json:"entities,omitempty"` // populated only when extracted
	Context       *string           `json:"context,omitempty"`
	OccurredStart *string           `json:"occurred_start,omitempty"`
	OccurredEnd   *string           `json:"occurred_end,omitempty"`
	MentionedAt   *string           `json:"mentioned_at,omitempty"`
	DocumentID    *string           `json:"document_id,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
}

// ---- Banks / admin ----
type Bank struct {
	BankID    string  `json:"bank_id"`
	Name      *string `json:"name,omitempty"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}
type BankSummary struct {
	BankID    string `json:"bank_id"`
	FactCount int    `json:"fact_count"`
	CreatedAt string `json:"created_at"`
}
type ListMemoriesResponse struct {
	Items  []RecallResult `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// ---- Setup / settings / health ----
type SetupRequest struct {
	Provider          string  `json:"provider"`
	APIKey            string  `json:"api_key,omitempty"`     // never serialized back
	Model             string  `json:"model"`
	Passphrase        string  `json:"passphrase"`
	BaseURL           *string `json:"base_url,omitempty"`
	EmbeddingProvider *string `json:"embedding_provider,omitempty"`
	EmbeddingModel    *string `json:"embedding_model,omitempty"`
}
type UnlockRequest  struct { Passphrase string `json:"passphrase"` }
type UnlockResponse struct {
	Unlocked     bool   `json:"unlocked"`
	SessionToken string `json:"session_token,omitempty"`
}
type Settings struct {
	Provider          string  `json:"provider"`
	Model             string  `json:"model"`
	BaseURL           *string `json:"base_url,omitempty"`
	EmbeddingProvider *string `json:"embedding_provider,omitempty"`
	EmbeddingModel    *string `json:"embedding_model,omitempty"`
	EmbeddingDim      int     `json:"embedding_dim"`
	APIKeySet         bool    `json:"api_key_set"`
	AllowOffline      bool    `json:"allow_offline"`
}
type HealthResponse struct {
	Status           string `json:"status"`
	Version          string `json:"version"`
	SetupComplete    bool   `json:"setup_complete"`
	Unlocked         bool   `json:"unlocked"`
	Portable         bool   `json:"portable"`
	ContentEncrypted bool   `json:"content_encrypted"` // D13
	Embedder         string `json:"embedder"`
	ChatProvider     string `json:"chat_provider"`
}
```

---

## 9. Embeddings (local) & LLM (optional)

**Decided by the user: everything runs locally, CPU-only, pure-Go.** Embeddings are computed
on-device; the LLM (for extraction/reflect) is *optional* and online-only — v1's default is no LLM
at all (D18). This keeps the stick fully self-contained: no network, no API key, no data leaving the
host for normal use.

### 9.1 Two interfaces
```go
type Embedder interface {                                                  // REQUIRED, always local
	Name() string
	Dim() int                                                              // pinned at first init
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
	EmbedQuery(ctx context.Context, texts []string) ([][]float32, error)
}
type Chat interface {                                                      // OPTIONAL (online only)
	Name() string
	Verify(ctx context.Context) error
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
}
```

### 9.2 Embedder — local CPU, pure-Go (PROVEN — §9.2.1)
- **Primary: `LocalEmbedder` via `cybertron` (on `spaGO`)** — pure-Go transformer inference on CPU,
  no CGo. Default model **`sentence-transformers/all-MiniLM-L6-v2` (384-dim)**. The converted
  `spago_model.bin` (~90 MB) is **bundled in `data/` on the stick** (one shared copy, NOT embedded
  into each binary) so there is no runtime download and it works fully offline.
- **Quality upgrade path (same machinery):** `multilingual-e5-small` or `bge-small` (also BERT
  encoders cybertron can load) for better/multilingual recall at a larger model file — a config swap,
  not a code change (D17).
- **`HashEmbedder` — last-resort fallback only** (deterministic FNV-1a feature hashing → pinned dim,
  L2-normalized). Used *only* if the bundled model file is missing/corrupt, so the semantic column
  still populates. No longer the default offline path (the local model is). Reversibility note moot
  now that the whole DB is encrypted (D13).
- Optional remote embedders (`OpenAIEmbedder`, `OllamaEmbedder`, OpenAI-compat) remain available for
  anyone who opts out of local, but are **off by default**.

#### 9.2.1 Verification spike — evidence (2026-06-06)
Go 1.26.4; `cybertron v0.2.1` + `spaGO v1.1.0`; loaded `all-MiniLM-L6-v2` and embedded on CPU:
- `CGO_ENABLED=0 go build` → `file` reports **"statically linked, not a dynamic executable."**
- Embedding **dimension = 384**.
- Semantic ranking is correct: for query *"How do I reset my forgotten password?"*, the two related
  docs scored **0.5916 / 0.3907** and the two unrelated **0.0620 / 0.0517** — clean separation.
- cosine via spaGO: `a.Normalize2().DotUnitary(b.Normalize2()).Item().F64()`.
- Model loads via `tasks.Load[textencoding.Interface]`; converted artifact `spago_model.bin` = 90 MB.
- Spike at `/tmp/embedspike/` → seed for the `internal/embed` regression test.

### 9.3 Chat (LLM) — optional, online-only
Default `NoneChat` (returns `ErrLLMUnavailable`) → fully-local v1 does **no** LLM extraction; retain
stores raw chunks, recall = keyword + semantic. If a user later configures a key, adapters exist:
`OpenAIChat` (`response_format: json_schema`), `AnthropicChat`, `OpenAICompatChat` (LM Studio/vLLM/
Ollama via base-URL), enabling fact/entity/temporal extraction + `reflect`. A **local** LLM is *not*
in v1 (it would mean CGo + multi-GB weights); see D18. Chat and Embedder are configured independently.

### 9.4 Dimension stability
Pin `dim` at first init from the active embedder (384 for MiniLM); persist in config; refuse to mix
dimensions. `HashEmbedder` emits the pinned dim so the fallback shares one column. Tag each `memory`
row with `embedder` (id + quality tier). Switching to a different-dim model later → see D16/D10
(reject at PATCH unless coercible; manual `reembed` to migrate).

### 9.5 (was `richlocal`) — superseded
The earlier `richlocal`/Hugot path (which needed a CGo tokenizer) is **dropped** — `cybertron`/`spaGO`
*is* the pure-Go local embedder it was trying to be, with its own pure-Go tokenizer, now proven (§9.2.1).

---

## 10. Build, cross-compile & USB realities

### 10.1 Cross-compile matrix (CGo OFF for every target)
```sh
build() {  # $1=GOOS $2=GOARCH $3=ext
  CGO_ENABLED=0 GOOS="$1" GOARCH="$2" \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" \
    -o "dist/$1-$2/joyvend$3" ./cmd/joyvend
}
build windows amd64 .exe ; build windows arm64 .exe
build darwin  amd64 ""    ; build darwin  arm64 ""
build linux   amd64 ""    ; build linux   arm64 ""
```
Opt-in: `-tags vec` (ncruces + WASM sqlite-vec, still CGo-off). **`-tags richlocal` is NOT in this
matrix** (needs CGo until a pure-Go tokenizer exists).

**No-CGo regression guard (†review):** CI runs the matrix with `CC=/bin/false` (forces a hard failure
if anything pulls in cgo) **and** a `go list -deps -f '{{.ImportPath}} {{.CgoFiles}}'` grep asserting
no `import "C"` in the default build's dependency graph.

### 10.2 Drive layout (flat: binaries + launchers at the root, data in joyvend_kb/)
```
<USB DRIVE>/
├── joyvend.cmd       # Windows: PROCESSOR_ARCHITECTURE + PROCESSOR_ARCHITEW6432; prefer arm64 if either reports ARM64, else amd64
├── joyvend.command   # macOS: exec joyvend-darwin-$(uname -m)
├── joyvend.sh        # Linux: exec joyvend-linux-$(uname -m)
├── joyvend-{windows,darwin,linux}-{amd64,arm64}[.exe]   # six platform binaries at the root
└── joyvend_kb/       # created on first launch; SHARED across all platforms
    ├── joyvend.db.enc
    ├── joyvend.config.json
    └── models/
```
Each binary resolves `joyvend_kb/` as a **sibling of itself** (its own dir = the drive root); no
walk-up. This separates code (regenerable binaries) from data (the encrypted KB). Total drive
footprint ≈ 6 × binary size (~15–25 MB each pure-Go) — another reason `richlocal` (+~90 MB each)
stays off by default. (Updated 2026-06-06: was `joyvend/bin/<os>-<arch>/joyvend` + `data/`.)

### 10.3 Code-signing realities
- **macOS Gatekeeper:** unsigned + quarantined → blocked. Doc the `xattr -dr com.apple.quarantine
  /Volumes/<DRIVE>/joyvend` workaround / right-click→Open. Proper fix: Developer ID + notarize (D11).
- **Windows SmartScreen:** unsigned `.exe` → "More info → Run anyway". Proper fix: Authenticode/EV.
- **Linux:** no Gatekeeper; FAT/exFAT have no exec bit → invoke via `sh joyvend.sh`.

### 10.4 USB / filesystem hazards
| Hazard | Reality | Mitigation |
|---|---|---|
| FAT32 4 GB single-file cap | growing DB hits 2³²−1 B → corrupt | Recommend **exFAT**; `doctor` warns on FAT32; refuse to grow DB past a safe threshold there. |
| Cross-OS FS | NTFS=mac read-only; APFS/HFS=Win unreadable | exFAT is the only out-of-box cross-OS read/write format. |
| No symlinks / exec bit on FAT/exFAT | — | Never CREATE symlinks (EvalSymlinks read is fine); launch via scripts. |
| Case-insensitive FS | Win/exFAT/mac default | Keep ALL bank isolation INSIDE the DB (`bank_id` column), never per-bank files. |
| Surprise removal | unflushed writes lost | DELETE journal + `synchronous=FULL` (**best-effort**) + clean-shutdown; **tell users to safe-eject**. |
| WAL sidecars fragile | `-wal`/`-shm` corrupt across replug | Default **DELETE** journal (single-file). |
| Two hosts / double launch | single-writer, no cross-host lock | Per-OS advisory lock (`flock`/`LockFileEx`) + refuse 2nd local launch; two-host mounting unsupported; `busy_timeout` absorbs benign races. |
| Slow USB random I/O vs scan | cold page reads | **Per-bank RAM cache (primary)**; `mmap`/`cache_size`/`temp_store=MEMORY` (secondary, best-effort); SQL prefilter; cap per arm; `doctor` cold-scan benchmark. |

### 10.5 Schema migration / versioning
Forward-only numbered SQL (`migrations/NNNN_name.sql`) embedded via `//go:embed`, each in its own
transaction. `schema_version` tracks `version` + `min_binary`. Startup: read version; if DB version >
highest embedded migration → **abort (fail closed)** ("this drive needs joyvend ≥ X"); else apply
higher migrations, bumping `version`/`min_binary` last. Per-migration transactions + `synchronous=FULL`
mean a yank mid-migration leaves a consistent earlier version. No down-migrations.

---

## 11. Config & security

### 11.1 Launch flow — password-before-serving, no locked state
`joyvend serve` resolves the data dir from the binary, then `IsFirstLaunch()` =
`os.Stat(ConfigPath())` is NotExist (file presence, not host state — replugging into a fresh machine
sees the file → NOT first launch).

**First launch ever (no config) → SETUP, creates the password:**
1. Prompt for a **decryption password (twice)** + accept the embedding-model default.
2. Derive KEK (argon2id), generate a random DEK, wrap DEK under KEK, write config atomically, create
   an empty encrypted `joyvend.db.enc`.
3. Print the copy-paste integration snippet → **start serving** (already unlocked).

**Every subsequent launch (config exists) → PROMPT to decrypt, then serve:**
1. **Always** prompt for the decryption password (no echo).
2. Derive KEK → unwrap DEK → `Deserialize` the decrypted DB into RAM. Wrong password → GCM auth fail
   → re-prompt (≤ N tries, with the persisted backoff) → **exit** on exhaustion.
3. On success: print the snippet → **start serving.**

**No "locked but running" state.** The server only ever runs *unlocked* — so there is **no runtime
`/unlock` or `/lock` endpoint and no `423 Locked` gating** (removed). Unlock is purely a startup step;
"locking" = stopping the process (which flushes + zeroizes the key). Idle-timeout → flush + shut down
(relaunch + password to resume).

**Headless (no TTY):** password from `JOYVEND_PASSPHRASE` (read once, then `os.Unsetenv`) or piped
stdin; if neither is present, **refuse to start** (do not boot locked).

> With no API key in the design (§0.0), the password's sole purpose is decrypting the DB.

### 11.2 Config file (`joyvend.config.json`, beside the binary)
```json
{
  "schema_version": 1,
  "llm":        { "provider":"none" },                       // optional, online-only; default off (D18)
  "embeddings": { "provider":"local", "model":"sentence-transformers/all-MiniLM-L6-v2",
                  "dimensions":384, "fallback":"hash" },     // local CPU via cybertron; model in data/
  "server":     { "addr":"127.0.0.1:8765", "require_session_token": true, "allow_nonloopback": false },
  "runtime":    { "allow_offline": true, "auto_lock_idle_minutes": 15, "soft_cap_mb": 450, "hard_warn_mb": 1024 },
  "secret": {
    "kdf":     { "algo":"argon2id", "time":4, "memory":262144, "threads":4, "key_len":32, "salt":"<b64-16B>" },
    "wrapped_dek": { "nonce":"<b64-12B>", "ciphertext":"<b64 GCM(DEK under KEK)>" },
    "api_key": { "nonce":"<b64-12B>", "ciphertext":"<b64 GCM(api_key under DEK)>" },
    "enc":     { "algo":"aes-256-gcm", "version":1 },
    "unlock_fail": { "count":0, "last_at":0 }
  }
}
```
Only the `ciphertext` fields are secret; KDF params + salt + nonces are plaintext (needed to derive).
Provider/base_url/model stay plaintext so settings render "configured, locked" before unlock.
**The entire `secret` envelope header (kdf algo+params+salt, enc algo+version, schema_version) is
bound as GCM AAD (†review)** so any parameter tampering is an auth failure, not a silent key change.

### 11.3 KDF + cipher (hardened †review)
- **KDF: argon2id** (`golang.org/x/crypto/argon2.IDKey`). Defaults **calibrated at setup** toward
  256 MiB–1 GiB memory under a time budget; **floor `memory=256 MiB`** (any supported host can
  allocate it); `threads` is **PINNED** to the stored value (never `runtime.NumCPU()`) so a stick
  moved between a 4-core and 16-core host derives the same key. All params stored plaintext.
- **Passphrase policy:** minimum length ≥ 12 + a small pure-Go entropy estimate; reject weak
  passphrases (or require explicit `--force`). The rate-limit does **nothing** against an offline
  crack — KDF cost + passphrase strength are the real protection.
- **Cipher: AES-256-GCM** (`crypto/aes` + `crypto/cipher`, stdlib). AEAD ⇒ wrong passphrase /
  tampered envelope fails on `Open()` (→ `401 ErrWrongPassphrase`, distinct from `423 Locked`).
  Fresh random 16-B salt + 12-B nonce on every save (setup + every rotation) — never reuse `(key,
  nonce)`. Optional compiled-in **pepper** mixed as additional AAD (limited value — binary is on the
  stick too — documented).

### 11.4 KEK/DEK split (†review)
Passphrase → **KEK**; a random **DEK** (`crypto/rand`) is wrapped under the KEK and stored; the
api_key (and, under D13, content) is encrypted under the DEK. A passphrase change only re-wraps the
DEK (cheap) instead of re-encrypting all data — essential for the future whole-DB-encryption path on
slow USB media. Use HKDF for distinct sub-keys (api_key vs content vs index) if needed.

### 11.5 Passphrase / key in memory (†review)
- Prompt once per launch via `golang.org/x/term.ReadPassword` (returns `[]byte`); keep it `[]byte`
  end-to-end — **never** a Go `string` (immutable, uncollectable, unzeroizable). Never `fmt`/log it.
- Hold the derived key + decrypted secrets in **`mlock`'d** memory (`x/sys/unix.Mlock` /
  `VirtualLock` on Windows), best-effort, to keep them out of swap; document that `mlock` may need
  privilege and recommend disabling/encrypting swap for the paranoid.
- Zeroize on lock/shutdown (honest caveat: Go's moving GC may leave stale copies).
- **Auto-lock on idle** (`auto_lock_idle_minutes`, default 15) and on detected drive removal.

### 11.6 Content-at-rest encryption — DECIDED: whole-DB (D13)
**The entire database is encrypted at rest** — content, FTS5 index, entity names, and embedding
vectors all live inside the encryption boundary (only a whole-DB approach covers the search
structures automatically; per-column would leave the FTS index as a plaintext copy). Mechanism:

**Persistence model — DECIDED: debounced whole-blob re-seal (D19, option 2).** No journal file.

**On-disk layout:** a single `joyvend.db.enc` — the whole SQLite DB, AES-256-GCM sealed under the DEK.
Nothing else; no plaintext DB ever touches the stick.

**Unlock:** decrypt `joyvend.db.enc` into an **in-RAM SQLite DB** (`:memory:`). FTS5 + `vec0` run on
the plaintext-in-RAM DB, so recall is unchanged. Recovery is trivial — the blob is always whole and
internally consistent (atomic rename), so there's nothing to replay.

**Save (`retain`):**
1. apply the insert to the in-RAM DB (FTS5 + vec0 update in RAM); ack the client immediately;
2. mark the DB **dirty** and arm a **debounced flush** (fires after `flush_idle_ms` ≈ 3–5 s of write
   inactivity, or immediately once `flush_max_writes` ≈ 200 unflushed writes accrue — whichever first);
3. **flush** = serialize the in-RAM DB → AES-256-GCM encrypt → **atomic** temp → `fsync` → rename over
   `joyvend.db.enc` → clear dirty. A burst of retains coalesces into **one** re-seal, so we never pay
   the O(DB-size) rewrite per write.

**Synchronous flush (cancels the debounce, guarantees no loss) on:** SIGINT/SIGTERM, `lock`,
idle-timeout, the safe-eject path, and `retain?sync=true` for callers that want durable-on-ack.

**Durability semantics:** a normal `retain` ack means *accepted + on disk within ~`flush_idle_ms`*.
The **only** loss scenario is a **hard power-loss / yanked stick** between flushes → at most the last
few seconds of memories. Clean shutdown / eject is **lossless**. (This is the simplicity/loss-window
trade chosen in D19; a journal for durable-on-ack is a backward-compatible future upgrade — an old
drive simply has no journal file.)

**Keying:** the DB blob is sealed with the KEK/DEK from §11.4 — the password-derived KEK unwraps the
DEK; the DEK seals the blob. One password unlocks everything.

**Implications:**
- The whole DB lives in **RAM** while unlocked → a **size ceiling** (§16). A **soft warning fires at
  `soft_cap_mb` (450 MB)** — surfaced in the `retain` response, `health`, `doctor`, and the startup
  banner — and a hard warning at `hard_warn_mb` (1 GB). At 384-dim, 450 MB ≈ ~240k memories. (Re-seal
  cost also grows with size; near the cap, flushes take seconds — another reason the cap warning exists.)
- **✅ VERIFIED (2026-06-06):** modernc exposes `conn.Serialize() ([]byte, error)` + `conn.Deserialize([]byte)`
  (reach via `sql.Conn.Raw` + an exported-method interface). A spike round-tripped a whole DB
  (content + FTS5 + vec0) through serialize → AES-256-GCM → decrypt → deserialize: the on-disk blob
  had **no plaintext**, and FTS5 MATCH + vec0 KNN worked on the restored in-RAM DB, built
  `CGO_ENABLED=0`. No `VACUUM INTO` fallback needed. Evidence in §11.6.1.
- `health.content_encrypted` now reports **true**; success-criterion #5 is fully met (a stolen
  powered-off stick yields only ciphertext for *everything*, not just the API key).

#### 11.6.1 Verification spike — evidence (2026-06-06)
Go 1.26.4 + `modernc.org/sqlite@v1.52.0`:
- modernc provides `conn.Serialize() ([]byte, error)` and `conn.Deserialize(buf []byte) error`
  (conn.go); reached from a `database/sql` connection via `sql.Conn.Raw(func(dc any) error{...})`
  asserting `dc` to an interface with those two exported methods (the concrete `*conn` is unexported).
- Built a source `:memory:` DB with a `memory` table, an external-content **FTS5** index, and a
  **vec0** vector table; `Serialize()` → 77,824 bytes in RAM.
- `AES-256-GCM` sealed → `joyvend.db.enc` (77,852 bytes). A byte-grep for the known memory string
  found **nothing** (on-disk artifact is pure ciphertext).
- `Open()` → `Deserialize()` into a fresh `:memory:` DB; `SELECT`, `FTS5 MATCH 'roommate'`, and
  `vec0 KNN` all returned the correct rows on the restored DB.
- `CGO_ENABLED=0 go build` → statically linked. Spike at `/tmp/cryptdbspike/` → seed for
  `internal/store/cryptdb_test.go`.

### 11.7 exe-dir resolution + read-only fallback
`dir = filepath.Dir(filepath.EvalSymlinks(os.Executable()))`. Detect go-run/temp
(`HasPrefix(exe, os.TempDir())` / `JOYVEND_DEV` / `JOYVEND_DATA_DIR`), macOS AppTranslocation
(`/AppTranslocation/`), and read-only mounts (atomic temp-write probe). On failure fall back to
`os.UserConfigDir()/joyvend` with `portable=false` + a loud warning that config + DB will NOT travel
this session (D6: warn, not refuse, for v1). Re-resolve at every startup (Windows drive letters
change). File perms `0600` on **both** config and DB; `0700` on any created host-fallback dir;
restrict ACL to the current user on Windows. On FAT/exFAT unix perms are ignored — **encryption, not
perms, is the real protection** (documented; reinforces D13).

### 11.8 No-secrets-in-logs (†review)
No `Stringer`/`MarshalJSON` on key/passphrase/token types; scrub `Authorization` headers from any
logged HTTP error; never log retain/recall bodies at info level. A test greps captured e2e log
output for the test api_key/passphrase/token and fails if found.

---

## 12. Implementation milestones

> Legend: `[ ]` task · **†review** = added/fixed by the adversarial review · each milestone ships
> green tests before the next starts.

### M0 — Scaffolding
**Goal:** empty repo → buildable, cross-compilable pure-Go skeleton that self-resolves its data dir.
- [ ] `go mod init joyvend.io`; add `modernc.org/sqlite v1.52.0`, `golang.org/x/crypto`, `golang.org/x/term`, `golang.org/x/sys`
- [x] **GATING SPIKE (D1) — DONE ✅ (2026-06-06, see §5.1.1).** Verified with Go 1.26.4 + `modernc.org/sqlite@v1.52.0`: blank-import auto-registers `vec0` (`vec_version()=v0.1.9`); cosine KNN matches brute-force ordering; builds static under `CGO_ENABLED=0`. Port the spike into `internal/vector` as a regression test.
- [ ] `internal/paths`: `os.Executable`→`EvalSymlinks`→`Dir`, walk up to `joyvend/` root; `DataDir/DBPath/ConfigPath/IsFirstLaunch/Portable`
- [ ] go-run/temp detection + AppTranslocation + read-only writability probe with host fallback
- [ ] `cmd/joyvend/main.go`: arg parse, dispatch `version` (version+SHA+SQLite version+`vec_available`), `serve`/`setup` stubs
- [ ] `Makefile` `build()` over the six targets, `-trimpath -ldflags='-s -w'`
- [ ] GitHub Actions: matrix build `CGO_ENABLED=0`
- [ ] **†review** no-CGo guard: `CC=/bin/false` build + `go list -deps` cgo-grep
- [ ] SMOKE: in-memory modernc SQLite `CREATE VIRTUAL TABLE t USING fts5(x)` (confirms FTS5 compiled in)
- [ ] **†review** runtime backend probe: attempt `CREATE VIRTUAL TABLE _probe USING vec0(e float[2])`; set `vec_available`; default to `vec0`, else brute-force fallback (correctness identical)

**Tests:** `paths_test` (faked exe under `bin/<os>-<arch>/` → `DataDir` is the shared `data/`;
temp-exe → `JOYVEND_DATA_DIR`; read-only dir → host fallback + `Portable()==false`); CI six-target
compile; `fts5_smoke_test`; **†review** no-CGo deps assertion.

### M1 — Config, setup & secret-at-rest
**Goal:** first-launch detection, KEK/DEK, passphrase-derived encryption, sticky config, unlock model.
- [ ] `Config` struct, `json:"-"` on secrets; the §11.2 envelope (KDF + wrapped_dek + api_key + enc + unlock_fail)
- [ ] `atomicWriteJSON` (temp+rename); **†review** `fsync` temp + dir; `0600`
- [ ] **†review** KEK/DEK: passphrase→KEK; random DEK; wrap DEK under KEK; api_key under DEK
- [ ] `secret.Seal`: random 16-B salt + 12-B nonce per save; `argon2.IDKey` **calibrated, threads PINNED**; **†review** full-envelope AAD
- [ ] `secret.Unlock`: re-derive KEK, unwrap DEK, GCM Open; auth fail → `ErrWrongPassphrase`
- [ ] **†review** `KeyStore`: `mlock`/`VirtualLock`, mutex, `Lock()` zeroizes; passphrase stays `[]byte`; `JOYVEND_PASSPHRASE` unset after read
- [ ] **†review** passphrase policy (≥12 + entropy check); reject weak unless `--force`
- [ ] `setup.go` interactive flow; **†review** shared anthropic-embedder validator (reused by HTTP setup)
- [ ] CLI: `setup` (refuse if config exists), `settings get/set`, `unlock`
- [ ] **D13** the DEK seals the **DB blob** (§11.6). With no API key (§0.0), the password's only job is DB encryption — KEK unwraps DEK, DEK seals `joyvend.db.enc`. *(M1 LLM/api_key remnants below — anthropic validator, `api_key`/`unlock_fail` envelope fields — are superseded by §0.0; drop during impl.)*

**Tests:** seal→open round-trip; wrong passphrase → `ErrWrongPassphrase`; tampered ciphertext / AAD
mismatch → fail; two saves → different salt+nonce; config save→reload preserves plaintext, never
writes secrets cleartext, no temp left on success; **†review** KDF determinism across simulated
4-vs-16 cores (threads pinned); **†review** passphrase-policy rejects weak; setup golden config shape.

### M2 — Storage, schema, migrations & whole-DB encryption (D13/D19)
**Goal:** an **encrypted-at-rest** SQLite store: decrypt-to-RAM on unlock, **debounced whole-blob
re-seal** on write (D19, no journal), forward-only migrations, per-OS single-instance lock, **defined concurrency model**.
- [x] **D13 GATING SPIKE — DONE ✅ (§11.6.1).** modernc `conn.Serialize`/`Deserialize` confirmed; whole-DB (content+FTS5+vec0) encrypt round-trip works, on-disk blob is ciphertext, static `CGO_ENABLED=0`. Port `/tmp/cryptdbspike` into `internal/store/cryptdb_test.go`.
- [ ] **D13** `store.OpenEncrypted`: decrypt `joyvend.db.enc` (AES-256-GCM, DEK) → in-RAM `:memory:` DB via `Deserialize`. No journal/replay — the blob is whole + atomic, so unlock is just decrypt+deserialize.
- [ ] **D19** persistence: mark-dirty on write + **debounced flush** (`flush_idle_ms` ≈ 3–5 s / `flush_max_writes` ≈ 200, whichever first) = `Serialize` → AES-256-GCM → atomic temp+`fsync`+rename `joyvend.db.enc` → clear dirty; coalesces bursts into one re-seal
- [ ] **D19** synchronous flush (cancel debounce) on SIGINT/SIGTERM, `lock`, idle-timeout, safe-eject, and `retain?sync=true`; ack is from RAM (durable within `flush_idle_ms`)
- [ ] `0001_init.sql` (all tables + `memory_fts` external-content + triggers + `schema_version`)
- [ ] `store.Open`: in-RAM DB PRAGMAs (`synchronous=OFF` is safe — durability is the periodic blob re-seal, not the RAM DB), `foreign_keys`, `temp_store=MEMORY`
- [ ] **†review** concurrency: serialized writer (`SetMaxOpenConns(1)` or write mutex) + concurrent reads; `RWMutex` on the embedding cache
- [ ] `migrate.go`: read version; db>embedded → abort fail-closed; else apply higher migrations per-txn, bump version+min_binary last
- [ ] **†review** per-OS single-instance lock: `flock` (unix) / `LockFileEx` (Windows); refuse 2nd launch
- [ ] `memories.go`: insert (with tags), get, delete, admin list (`type/q/tags/limit/offset`, `ORDER BY event_at DESC, created_at DESC`)
- [ ] `fts.go`: bm25 MATCH joined for bank/tag filter
- [ ] `vector/encode.go`: float32↔BLOB little-endian; unit-normalize + store norm
- [ ] **D1** `vector/vec0.go`: blank-import `modernc.org/sqlite/vec`; per-bank `vec0` vtable; insert mirror + KNN `MATCH` filtered by `(bank_id, model)` — the **default** path
- [ ] **D1** `vector/bruteforce.go`: exact cosine fallback over the `embedding` BLOBs, `(bank_id, model)`-filtered, RAM-cached — selected when the `vec0` probe fails; both behind one `VectorIndex` interface
- [ ] `banks.go`: lazy create; list with fact_count; PUT upsert; DELETE cascade (drops the bank's `vec0` rows too)

**Tests:** Open creates DB; journal read-back == chosen or DELETE fallback; migrate fresh→v1,
idempotent, v=99 → abort with min_binary; **†review** real N→N+1 migration fixture (test-tag 0002
adds a column → pre-existing rows survive, FTS consistent, version+min_binary bump); FTS MATCH order
+ delete sync; **†review** `encode_test` asserts fixed bytes (`float32(1.0)`→`00 00 80 3F`);
per-OS lock 2nd-Open fails; admin pagination + tag filter; **†review** `-race` concurrency test
(N goroutines retain+recall one bank, no corruption/race); **D1 parity test**: on a known vector set
`vec0` KNN and the brute-force fallback return the *same* top-K (they must — both are exact);
**D1**: a bank mixing `hash` + model embeddings returns only same-model vectors per arm;
**D13/D19**: write → debounced flush → reopen recovers the memory; **flush coalescing** (a burst of M
writes yields exactly one re-seal); **hard-yank** before flush loses only the unflushed burst, but a
**sync flush / clean shutdown loses nothing**; `retain?sync=true` is durable on ack; the on-disk
`joyvend.db.enc` contains **no plaintext** (byte-grep for a known memory string fails).

### M3 — Embedder (local) + optional Chat
**Goal:** the local pure-Go CPU embedder wired in (proven §9.2.1); Chat optional/off by default;
dim pinning. *(Freeze `domain/types.go` first.)*
- [ ] **†review** freeze `domain/types.go` (§8.2) + golden marshal test asserting exact key sets
- [ ] **D9** `embed.LocalEmbedder` via `cybertron`/`spaGO` (`all-MiniLM-L6-v2`, 384-dim, mean pooling); port the §9.2.1 spike into an `internal/embed` regression test
- [ ] **D9** ship the converted `spago_model.bin` in `data/` (bundle on the stick); checksum at startup; **no runtime download** (set `DownloadPolicy`/`ConversionPolicy` only as a dev/setup convenience)
- [ ] `HashEmbedder` (FNV-1a feature hashing, pinned dim) as **last-resort fallback only** (model file missing/corrupt); drop the semantic arm when it's active (D15)
- [ ] optional remote embedders (`OpenAIEmbedder`/compat/`Ollama`) behind config, **off by default**
- [ ] `llm.Chat` + `NoneChat` (default); optional `OpenAIChat`/`AnthropicChat`/`OpenAICompatChat` adapters for when a key is added (D18)
- [ ] dim pinning: first init records `Dim()` (384); refuse mismatched dim later
- [ ] startup banner: active embedder (`local:all-MiniLM-L6-v2` / `hash-fallback`) + chat provider (`none`)

**Tests:** **†review** golden marshal (TokenUsage/Settings/HealthResponse/Bank/BankSummary/
ListMemoriesResponse exact keys); **D9** `LocalEmbedder` produces 384-dim vectors and ranks a related
doc above an unrelated one (the §9.2.1 assertion); `HashEmbedder` deterministic + L2-norm + dim;
`NoneChat.Complete`→`ErrLLMUnavailable`; optional-adapter tests vs `httptest` stub; dim mismatch refused.

### M4 — Ingest (retain) pipeline
**Goal:** retain end-to-end. **Default (local, D2/D18): chunk → local embed → store** (no LLM); the
extract path is the optional online add-on.
- [ ] `chunk.go`: RecursiveCharacter, 3000 chars, no overlap; JSON-conversation turn-aware path
- [ ] **DEFAULT** `ingest.go` local: one verbatim unit per chunk (enriched=0, event_at=timestamp|now) → **`LocalEmbedder`** → insert memory + tags + embedding (+ vec0) in one txn; `usage` nil
- [ ] **optional/online** `extract.go` (only when a Chat key is set): pin extraction JSON schema; parse + repair pass; zero facts → store raw chunk; **†review** per-item transactionality + partial-failure policy (degrade chunk to raw OR abort item — pick + test); **usage aggregated** across chunk calls
- [ ] **optional/online** `ingest.go` enriched: extract → embed per fact → insert memory(enriched=1)+tags+embedding+entity/edge in one txn
- [ ] timestamps: ISO | nil(now) | "unset"(NULL); metadata JSON1; manual `EntityInput` merged with auto
- [ ] `RetainResponse.usage` non-nil only when the LLM ran; **†review** include the soft-cap `warning` field when DB size > `soft_cap_mb`

**Tests:** chunk (7000 chars→3, no overlap; JSON turns split); **default local** (LocalEmbedder): 1 row/chunk
enriched=0, 384-dim embedding present, usage nil, vec0 populated; **optional online** (stubbed Chat): N rows
enriched=1, entities upserted, usage populated+aggregated; timestamp variants; **†review** extraction
malformed-JSON → repair → defined fallback; **†review** retain near `soft_cap_mb` returns a `warning`;
re-retain doesn't duplicate beyond expected.

### M5 — Retrieval (recall) pipeline
**Goal:** recall end-to-end: parallel arms → RRF(k=60) → passthrough rerank + recency → token budget.
- [ ] `fusion.go`: RRF k=60 (mirror hindsight `fusion.py`)
- [ ] keyword arm (bm25, bank/tag-filtered, skip if no tokens)
- [ ] **D1** semantic arm: `EmbedQuery` → `vec0` KNN (default) **filtered by `(bank_id, model)`**; brute-force cosine over RAM-cached vectors as the fallback when `vec0` is unavailable — both behind the `VectorIndex` interface
- [ ] **†review** temporal arm scoped to the **closed pattern set** (D14): ISO dates, `YYYY`, `Month YYYY`, `last/this/next week|month|year`, `N days/weeks/months ago`; unsupported → arm absent
- [ ] `tags_match`: any|all|any_strict|all_strict (strict excludes untagged); default any
- [ ] rerank-lite: `base=1−0.9·i/(n−1)`; recency 365d→[0.1,1.0] (0.5 if none); `weight=base·(1+0.2·(recency−0.5))`
- [ ] **†review** `k_final = clamp(max_tokens/AVG_FACT_TOKENS, 5, 100)` (AVG_FACT_TOKENS=64); cap 300; take `k_final·2`; greedy budget break-on-overflow
- [ ] **†review** recall mapping joins `memory→memory_entity→entity` to populate `RecallResult.entities`
- [ ] `trace=true` → per-arm ranks + rrf_score + weight; never in result body

**Tests:** **†review** RRF golden vs hand-computed (incl. a doc in one arm only — averaged down);
keyword-only (offline) returns FTS+temporal fused; full (stubbed embedder) fuses all three, a
two-arm doc beats a one-arm doc; tags_match (any_strict excludes untagged, all requires all);
**†review** `k_final` derivation + token budget break (not skip); recency tiebreak; **†review**
temporal closed-set positive + negative (unsupported → arm absent, no crash); **†review** online
recall populates `entities`, offline returns `[]`; **†review** semantic arm returns only same-model
vectors when a bank mixes `hash` + remote.

### M6 — HTTP API
**Goal:** `net/http` server exposing the full v1 surface with gating, loopback/Host guard, CSRF.
- [ ] router under `/v1`; bind `127.0.0.1`; **†review** reject non-loopback unless explicit flag (log loudly); validate `Host` header against loopback literals
- [ ] gating middleware: `!setup_complete`→409; `setup_complete && !unlocked`→423
- [ ] handlers: `GET /health` (always), `POST /setup` (409 if config exists; **†review** shared anthropic validator), `POST /unlock` (401 bad; **†review** `crypto/rand` token, `ConstantTimeCompare`, TTL; rate-limit 5/min + backoff; **†review** persisted fail counter), `POST /lock`, `GET/PATCH /settings`
- [ ] **†review** `PATCH /settings` dim-conflict → 400 (field=embedding_model) unless coercible to pinned dim
- [ ] banks: `GET /banks`, `PUT/DELETE /banks/{id}`; bank_id regex
- [ ] memory: retain, recall, `GET memories` (paginated), `GET/DELETE memories/{id}`, `reflect`→501
- [ ] session-token bearer enforcement on bank routes (config flag, default ON); **†review** Origin/Referer or custom-header CSRF check
- [ ] `server/client.go`: thin CLI HTTP client; auto-unlock prompt flow
- [ ] wire retain/recall to ingest/retrieval; uniform error envelope

**Tests:** health before setup → `setup_complete=false`; bank route → 409; after setup lock→423,
unlock(bad)→401, unlock(good)→200+token; retain→recall round-trip over HTTP; reflect→501;
non-loopback refused; **†review** spoofed `Host` header rejected; **†review** HTTP setup test
(POST→200, second POST→409, anthropic+no-embedder→400); validation 400s; pagination.

### M7 — CLI & packaging
**Goal:** full CLI surface + the all-binaries drive layout with launchers.
- [ ] `serve` (first-launch auto-setup on TTY; `--addr`, `--offline`)
- [ ] `doctor` (data dir + portability, FS type, journal mode, embedder tier, chat reachability, schema_version, lock status, FTS5 smoke, **†review** cold-scan benchmark, **†review** stale `*.tmp` cleanup)
- [ ] CLI memory ops as thin HTTP clients (`--server`, `--json`, exit codes)
- [ ] `Makefile dist`: build six → `bin/<os>-<arch>/`, copy launchers, assemble `joyvend/` tree
- [ ] launchers detect OS/arch and exec; **†review** `joyvend.cmd` uses `PROCESSOR_ARCHITECTURE`+`PROCESSOR_ARCHITEW6432` (prefer arm64 if either reports ARM64)
- [ ] `README` (exFAT, `xattr` quarantine, SmartScreen, safe-eject); `SECURITY.md` (threat model + D13 status)

**Tests:** `version` fields; `doctor` reports portable + journal mode; **†review** launcher shell
test incl. Windows arch-detection branch; **†review** paths test asserting `os.Executable()` resolves
to `bin/<os>-<arch>/joyvend` when invoked **via each launcher** (the load-bearing resolver);
`make dist` produces the expected tree; gated e2e (`-tags e2e`): build linux binary, `serve` in a
temp drive, retain+recall via CLI.

### M8 — Hardening & cross-platform/offline validation
**Goal:** validate every locked premise end-to-end; harden the USB + secret edges.
- [ ] clean-shutdown handler (SIGINT/SIGTERM): flush, `PRAGMA optimize`, zeroize key, release lock, close
- [ ] **†review** auto-lock on idle + on detected drive removal
- [ ] cross-host replug simulation: config+DB in dirA, fresh process → no re-setup, unlock works, recall returns prior data
- [ ] offline e2e: `provider=none` → retain chunk, recall keyword+temporal, reflect→err, **no network**
- [ ] read-only mount: data dir `chmod a-w` → host fallback + `portable=false`; **†review** warn secret-file landed on host
- [ ] mid-migration safety: inject a failing 2nd migration → DB stays at v1, restart recovers
- [ ] FAT32 file-size guard: simulate >threshold → doctor warns / write refused
- [ ] D10: rows tagged with embedder/quality tier (re-embed deferred)
- [ ] **†review** no-secrets-in-logs: grep captured e2e logs for test key/passphrase/token

**Tests:** offline integration (asserts **no outbound network** via a dial-failing `Transport`);
replug; shutdown flush + lock release + key zeroized; **†review** unlock rate-limit 6th→429 +
persisted counter survives restart; read-only fallback; **†review** `Host`-header/DNS-rebinding
rejected; golden API contract fixtures (retain/recall/health/settings); **†review** log-scrub grep.

---

## 13. Test strategy

- **Unit (fast, no network):** paths, secret seal/open + KDF determinism + KEK/DEK, config round-trip,
  vector BLOB encode/decode (**fixed-byte assertion**), RRF golden, `HashEmbedder`
  determinism+similarity, chunker, temporal closed-set, token-budget + `k_final`. Table-driven.
- **Store/integration (real modernc, `t.TempDir()`):** migrations idempotency + fail-closed +
  **real N→N+1**, FTS MATCH + trigger sync, brute-force cosine known-answer + **model-scoped**,
  per-OS single-instance lock, admin pagination, **`-race` concurrency**.
- **Provider adapters (`httptest`):** assert outbound request shape + parse canned responses —
  no real key/network.
- **HTTP API (`httptest`):** gating (409/423/401), retain→recall round-trip, validation 400s,
  reflect 501, loopback + `Host`-header refusal, HTTP setup (409/anthropic-400), pagination.
- **End-to-end (`-tags e2e`):** build linux binary, `serve` against a temp drive, exercise CLI;
  shell test for launcher selection (incl. Windows arch branch).
- **Cross-platform CI:** the six-target `CGO_ENABLED=0` matrix + no-CGo guard on every push;
  **committed macOS + Windows jobs** for paths/launcher/doctor (not optional — portability is the
  whole point).
- **Local-default mode:** full retain→recall with `llm=none` + `LocalEmbedder`, enforced no-network
  (dial-failing `Transport`); plus a model-missing run that falls back to `HashEmbedder`.
- **USB-simulation:** read-only mount, mid-migration failure, cross-host replug, single-instance,
  FAT32 guard.
- **Security:** wrong-pass/tamper/AAD, KDF determinism, log-scrub grep, session-token TTL/compare.
- **Running:** all `go test ./...`; one pkg `go test ./internal/retrieval`; one test
  `go test ./internal/retrieval -run TestRRF -v`; `go test -race ./...`; tiers
  `go test -tags vec ./internal/store`; e2e `go test -tags e2e ./...`.

---

## 14. Milestone dependency graph

```
M0 ─▶ M1 ─▶ M2 ─▶ M4 ─▶ M5 ─▶ M6 ─▶ M7 ─▶ M8
        │      │     ▲
        └─▶ M3 ┴─────┘   (M3 freezes domain/types.go; needed by M4 ingest & M6 handlers)
```
M3 may proceed in parallel with M2 once M1 lands, but `domain/types.go` must be frozen before M4/M6.

---

## 15. Open decisions (edit these — they drive the milestones)

> `[x]` = accept the recommendation as written · edit the text to choose otherwise.
> **D1, D13 are decided by you.** Remaining open items (D17/D18/D19) ship with working defaults —
> nothing here blocks M0.

- [x] **D1 — Vector backend under pure-Go SQLite (AMENDS the locked `sqlite-vec` decision). DECIDED.**
  **Default = `modernc.org/sqlite/vec` (`vec0` KNN), pure-Go, vectors inside the same `.db`.**
  Brute-force exact cosine is retained as the automatic fallback (and correctness oracle); `ncruces`+
  WASM `-tags vec` is an alternative-driver escape hatch. Honors the intent (vectors in one SQLite
  file, static no-CGo binary) and supersedes the impossible C-extension form. *Both paths are exact
  (vec0 is an exact SIMD linear scan), so this is a speed/ergonomics choice, not an accuracy one.*
  **✅ Verified by spike (2026-06-06, §5.1.1):** `modernc/vec` registers `vec0` at v1.52.0
  (`vec_version()=v0.1.9`), cosine KNN matches brute-force, builds static under `CGO_ENABLED=0`.
- [x] **D13 — Encrypt memory content at rest. DECIDED: whole-DB.** The entire `.db` is an AES-256-GCM
  blob (`joyvend.db.enc`); unlock → in-RAM SQLite (FTS5 + vec0 on plaintext); writes → RAM + a
  **debounced whole-blob re-seal** (D19); one password-derived KEK/DEK unlocks everything. Covers
  content + FTS + vectors. Cost = whole DB in RAM → size ceiling + 450 MB warning. Full design §11.6.
  (Serialize/deserialize **verified**, §11.6.1.)
- [x] **D2 — Write-time extraction. DECIDED: the AGENT does it, not joyvend (§0.0).** joyvend has no
  LLM. retain stores raw content + a local embedding; if the calling agent extracts entities/facts
  with its own model, it passes them in via `MemoryItem.Entities`. No joyvend-side extraction.
- [ ] **D3 — Numeric score on `RecallResult`?** *Rec:* no score in body (hindsight-faithful); scores
  in `trace` only. Without a cross-encoder a single number over-promises.
- [ ] **D4 — Ship the graph-traversal recall arm in v1?** *Rec:* defer to v2; schema stays
  graph-ready (entity/edge populated when extraction is on); RRF degrades cleanly.
- [ ] **D5 — Forgotten-passphrase recovery?** *Rec:* no recovery by design (re-run setup with a new
  key). Optionally offer a clearly-labeled plaintext-no-encryption mode, never default.
- [ ] **D6 — Read-only stick: refuse or proceed with warning?** *Rec:* proceed with `portable=false`
  + loud warning for v1; make strict mode a config flag.
- [ ] **D7 — GCM AAD binding.** *Rec:* bind the **full secret-envelope header** (not just
  `schema_version`) to detect any KDF/cipher-param tampering as an auth failure.
- [ ] **D8 — Per-session bearer token on bank routes?** *Rec:* yes, default ON (loopback alone
  doesn't stop a malicious local process or browser CSRF); `crypto/rand` + `ConstantTimeCompare` + TTL.
- [x] **D9 — Local offline embedder. SUPERSEDED → DECIDED.** The Hugot/`richlocal` path (CGo tokenizer)
  is dropped; instead the embedder is **`cybertron`/`spaGO`, pure-Go, CPU, `all-MiniLM-L6-v2` (384-dim)**,
  bundled model in `data/`. **Proven** (§9.2.1): static `CGO_ENABLED=0` build, correct semantic ranking.
- [ ] **D10 — Lower-quality hash-embedded rows after reconnect?** *Rec:* v1 leave as-is but tag each
  row with embedder id + quality tier (in schema); add a manual `reembed` later; auto re-embed is v2.
- [ ] **D11 — Sign/notarize binaries for v1?** *Rec:* ship unsigned with documented bypass; signing
  is a release-eng task — revisit before any public distribution.
- [ ] **D12 — Token counting for `max_tokens`?** *Rec:* `chars/4` heuristic for v1 (budget is
  advisory); swap in a Go BPE port later if precision matters.
- [ ] **D14 — Temporal extraction scope.** *Rec:* enumerate a small **closed** pattern set (ISO,
  `YYYY`, `Month YYYY`, `last/this/next week|month|year`, `N days/weeks/months ago`); everything else
  yields no temporal arm. (hindsight uses Python `dateparser` — no pure-Go equivalent.)
- [x] **D15 — `HashEmbedder` role. RESOLVED.** No longer the default offline path (the local model is).
  It's a **last-resort fallback** only if the bundled model file is missing/corrupt; when active, drop
  the semantic arm to avoid double-counting BM25 in RRF.
- [ ] **D16 (dim-on-PATCH)** — switching embedding model at a **different** native dim via
  `PATCH /settings`. *Rec:* `400` (field=`embedding_model`) unless coercible to the pinned dim;
  same-dim swaps succeed.
- [x] **D17 — Embedding model. DECIDED: `BAAI/bge-small-en-v1.5` (384-dim), verified.** Loads in
  cybertron (BERT/WordPiece, **CLS pooling**), pure-Go CPU, sane ranking (related 0.69 vs unrelated
  0.42). `all-MiniLM-L6-v2` stays a lighter alternative. ⚠️ `multilingual-e5-small` uses XLM-R/
  SentencePiece — **not** confirmed in cybertron v0.2.1; needs its own spike if multilingual matters.
- [x] **D18 — LLM in joyvend? DECIDED: NO (§0.0).** joyvend ships with no LLM, no Ollama, no remote
  API, no API key. The calling agent is the LLM; it does all reasoning via MCP tools. (Superseded the
  earlier Ollama-companion idea — unnecessary once the consumer is itself an agent.)
- [x] **D19 — Encrypted-DB re-seal cadence. DECIDED: debounced whole-blob re-seal (option 2).** No
  journal. retain → RAM + ack → debounced flush (~3–5 s idle / 200 writes) re-seals the whole blob
  atomically; sync flush on shutdown/lock/idle/eject/`?sync=true`. Loss window = last few seconds only
  on a hard yank; clean eject is lossless. A journal (durable-on-ack) is a backward-compatible future
  upgrade if heavy-write/large-DB usage ever warrants it. See §11.6.
- [x] **D20 — Agent integration. DECIDED: pure REST + copy-paste snippet, NO MCP.** joyvend is a local
  loopback REST API; on start (and via `joyvend snippet`) it prints a paste-ready block the user drops
  into their AI client, which then calls the API with its shell/fetch tool. No MCP server, no skill
  file. Default loopback + Host-guard, session token off by default (`require_token` to enable).

---

## 16. Risks & mitigations

| Sev | Risk | Mitigation |
|---|---|---|
| ~~critical~~ → resolved | Memory content plaintext on a loseable stick. | **Resolved by D13**: whole-DB AES-256-GCM encryption (content + FTS + vectors); `health.content_encrypted=true`; success-criterion #5 fully met. |
| high | Whole-DB-in-RAM **size ceiling** (the cost of D13): a large store exhausts host RAM. | Soft warning at `soft_cap_mb` (450 MB) in retain/health/doctor/startup; hard warning at 1 GB; 384-dim keeps ~240k memories under the soft cap; documented as a personal-scale tool. |
| medium | **Re-seal latency** grows with DB size (whole blob re-encrypted on a slow stick). | D19 debounced flush coalesces bursts (one re-seal, off the ack path); near the 450 MB cap a flush takes seconds → the cap warning covers it; a journal (flat write cost) is the backward-compatible upgrade if needed. |
| low | Hard yank / power-loss between flushes loses the last unflushed burst (D19 trade). | Loss window = `flush_idle_ms` (~3–5 s); sync flush on shutdown/lock/idle/eject makes clean removal lossless; `retain?sync=true` for durable-on-ack; document "safe-eject to be sure". |
| medium | Pure-Go CPU transformer inference (cybertron) slower than native; first model load adds startup latency. | MiniLM is small (22M) → embeddings are fast for short texts; load the model once at unlock; `doctor` benchmarks embed latency; e5-small/bge-small only if quality needs it. |
| low | Bundled model (~90 MB `spago_model.bin`) adds to drive footprint; a missing/corrupt file breaks the semantic arm. | One shared copy in `data/` (not per-binary); checksum at startup; `HashEmbedder` fallback keeps recall working (keyword-led) if absent. |
| high | WAL/`-shm` sidecars corrupt on exFAT/FAT after surprise removal / cross-host replug. | Default DELETE journal (single-file), `synchronous=FULL`, clean-shutdown; WAL only on known-good local FS; document safe-eject. |
| high | argon2 `threads` from `NumCPU()` → undecryptable after moving hosts. | PIN all KDF params (incl. threads) to stored plaintext; determinism test (4 vs 16 cores). |
| high | argon2 work factor (64 MiB) too weak for an **offline** crack of a stolen envelope. | Calibrate toward 256 MiB–1 GiB (floor 256 MiB); passphrase-strength policy; rate-limit is only an online speed bump. |
| high | Same-uid local process reads the decrypted key from RAM / loopback API; browser CSRF/DNS-rebinding to localhost. | Document the inherent same-uid trust; session token (`crypto/rand`+`ConstantTimeCompare`+TTL); `Host`-header + Origin checks; reject wildcard binds; auto-lock; `mlock`. |
| medium | Derived key/passphrase paged to **host** swap (defeats "nothing host-side"). | `mlock`/`VirtualLock` best-effort; passphrase `[]byte`-only; `JOYVEND_PASSPHRASE` unset after read; document encrypted-swap advice. |
| low (was medium) | `modernc/sqlite/vec` (the **default** backend) unstable, or FTS5 unexpectedly not compiled in. | **Resolved by spike (§5.1.1):** `vec`+`vec0` confirmed working at v1.52.0 under `CGO_ENABLED=0`, KNN == brute-force. Residual risk is only future API drift; retained brute-force fallback (same exact result) keeps the semantic arm working regardless; `ncruces`+WASM `-tags vec` is a second escape hatch; M0 FTS5 smoke covers FTS5. |
| medium | FAT32 4 GB single-file cap silently corrupts a growing DB. | Recommend exFAT; `doctor` warns on FAT32; refuse to grow past a safe threshold; all bank isolation in one DB. |
| medium | Two hosts mount one drive / double launch races the file. | Per-OS advisory lock + refuse 2nd local launch; two-host mounting unsupported; `busy_timeout` for benign races. |
| medium | In-process concurrency (multi-goroutine server, single-writer file, shared cache) → races/corruption. | Serialized writer + `RWMutex` cache; `-race` concurrency test. |
| ~~medium~~ → resolved | `HashEmbedder` reversible / FTS+vectors content-derived. | Whole-DB encryption (D13) puts FTS + vectors inside the boundary; `HashEmbedder` is now a rarely-used fallback. |
| medium | Unsigned binaries blocked by Gatekeeper/SmartScreen → "plug and run" fails for non-technical users. | Document bypass for v1 (D11); plan notarization + Authenticode before public distribution. |
| low | First recall after replug is cold/I/O-bound (seconds at 100k on a slow stick). | Per-bank RAM cache primary; `doctor` cold-scan benchmark; set expectations in docs. |
| low | No cross-encoder lowers precision vs hindsight. | Ship verified RRF-passthrough + recency; leave a clean reranker seam for v2. |
| low | Go snippets unverified (no compiler in this env). | M0 first task is `go build`; every milestone gates on green tests. |

---

## 17. Appendix — what the adversarial review changed

Three independent reviewers (portability/no-CGo, security, completeness/fidelity) audited the
synthesized design. Verdicts: portability **ready** (with 4 must-fixes, all folded in); security
**not ready** without the D13 decision + KDF/memory/token hardening (folded in); completeness
**not ready** until the broken domain types, temporal/`HashEmbedder`/`extract` specs, and the
concurrency model were tightened (folded in). They independently **verified** the load-bearing
facts: FTS5 is in modernc v1.52.0; `modernc/sqlite/vec` exists and is pure-Go (ccgo, not CGo);
hindsight's RRF `k=60` + 300-cap + passthrough rerank formula + recency model + the four
`tags_match` modes. Every change is tagged **†review** at its point of use above.

*End of plan. Edit §15 (and §0.1) and tell me which decisions to lock; I'll fold your choices in and
we can start M0.*
