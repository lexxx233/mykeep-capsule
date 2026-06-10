<!--
GitHub topics to set in the repo's "About" sidebar (boosts discoverability):
ai-memory, agent-memory, llm-memory, local-first, privacy, encryption, vector-search,
semantic-search, embeddings, rag, claude, cursor, self-hosted, portable, golang, no-cgo
-->

<div align="center">

# 🧠 mykeep · Capsule

### Portable, encrypted memory for AI agents — on a USB drive.

One pure-Go binary and one encrypted file. Plug it into any machine, type your password, and
your agent gains a private, persistent, **semantic memory** it talks to over a tiny local API.
No cloud. No API keys. No database to run.

![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)
![pure Go · no CGo](https://img.shields.io/badge/pure%20Go-no%20CGo-00ADD8)
![platforms](https://img.shields.io/badge/platforms-Windows%20%7C%20macOS%20%7C%20Linux-555)
![at rest](https://img.shields.io/badge/at%20rest-AES--256--GCM-2ea043)
![local-first](https://img.shields.io/badge/local--first-no%20cloud%20%C2%B7%20no%20keys-1f6feb)
![license](https://img.shields.io/badge/license-MIT-blue)

[mykeep.ai](https://mykeep.ai) · **Secured · Private · Portable**

</div>

---

Capsule is the **"knows you"** component of [mykeep](https://mykeep.ai): a self-contained memory
server for LLM agents (Claude Code, Cursor, or anything with a shell or fetch tool). It stores
what your agent learns, searches it semantically, and encrypts everything at rest — all on the
drive, with **zero host dependencies and no CGo.** The agent does the reasoning; Capsule just
remembers.

> Think of it as a private, encrypted, portable alternative to cloud "memory" features and
> heavyweight RAG stacks — without sending your data anywhere or running a database.

## Why it's different

- 🔒 **Encrypted at rest.** The *entire* database — content, search index, and vectors — is one
  AES-256-GCM blob, sealed with an argon2id key from your password. A lost drive yields only
  ciphertext. No recovery, by design.
- 🧲 **Three-way recall.** Semantic (local `bge-small` embeddings, vec0 KNN) + keyword (BM25
  full-text) + a date-aware temporal arm, fused with Reciprocal Rank Fusion and a recency boost.
- 💻 **Depends on nothing.** One static binary — no installer, no host services, no CGo.
  Cross-compiles to win/mac/linux × amd64/arm64 from a single host.
- 🤖 **No LLM, no keys.** Capsule stores and retrieves; *your* agent does the thinking. Nothing
  calls the cloud, and there's no API key to leak.
- 🧳 **Truly portable.** Config and data live next to the binary on the drive, so moving the
  drive carries your whole memory with it.

## How it works

```
 your AI agent  ──HTTP──▶  Capsule (local, loopback)  ──▶  encrypted SQLite on the USB drive
 (Claude Code,            retain / recall / search          AES-256-GCM · FTS5 · vec0 vectors
  Cursor, …)              local CPU embeddings               decrypted into RAM while unlocked
```

The agent already *is* the LLM, so Capsule runs no model of its own (beyond the small local
embedder used for search). You connect it by pasting a one-paragraph snippet into your
assistant — no MCP server, no plugin, no config files.

## Quick start

```sh
git clone https://github.com/lexxx233/mykeep-capsule.git && cd mykeep-capsule
go build ./cmd/mykeep      # or: make build  ->  bin/mykeep
./bin/mykeep               # opens the GUI in your browser (double-click on a drive)
```

On **first launch** you create a password; every launch after, you're prompted for it (the DB
is decrypted into RAM, then served). Then paste this block — printed on launch, or via
`mykeep snippet` — into your AI assistant:

```
You have a persistent local memory (mykeep) at http://127.0.0.1:8765.
▶ First, fetch your instructions:  GET http://127.0.0.1:8765/v1/guide
Then follow them — remember facts about the user/project as you learn them, and
recall before you answer. Use your shell or fetch tool to call the API.
```

The agent fetches its full operating manual from `/v1/guide` (the retain / recall / reflect /
supersede protocol), then just `curl`s the local API. That's the whole integration. (For chat
clients that can't fetch, the GUI's **"Copy full instructions"** button and `mykeep guide`
print the manual inline.)

## Automatic capture (optional)

Relying on the agent to *remember* to save things is the one weak spot of "the agent does the
reasoning." The fix keeps Capsule LLM-free but makes retention automatic:

- A host hook calls `mykeep capture` each turn to log the raw exchange as a low-tier, deduped
  `capture` memory — a safety net.
- An auto-triggered nudge asks the agent to periodically **distill** those into curated
  `mental_model`s.
- Captures stay hidden from normal recall until distilled (or via `recall {"include_captures": true}`).

Drop-in recipes live in **[`integrations/`](integrations/)** — Claude Code `UserPromptSubmit` +
`Stop` hooks, and a generic shell wrapper. They're non-fatal: if Capsule is stopped, capture is
silently skipped and the turn proceeds.

## CLI

```sh
mykeep                 # default: open the GUI
mykeep serve           # terminal mode (prompts for the password; great over SSH)
mykeep snippet         # reprint the paste-into-your-agent block
mykeep guide           # print the full agent operating manual (also GET /v1/guide)
mykeep doctor          # diagnostics (no password needed)
mykeep capture "..."   # log a raw turn (auto-retain; used by the hooks)
mykeep retain "..."    # add a memory          (talks to a running server)
mykeep recall "..."    # search your memories
mykeep memories        # browse
mykeep banks           # list memory banks
mykeep version
```

Headless: set `MYKEEP_PASSPHRASE` (or pipe it on stdin) for `serve`.

## HTTP API (loopback only)

| Method | Path | Purpose |
|---|---|---|
| `GET`  | `/v1/health` | status, embedder, memory count |
| `POST` | `/v1/banks/{bank}/retain` | store memories |
| `POST` | `/v1/banks/{bank}/capture` | auto-log a raw turn (deduped `capture` memory) |
| `POST` | `/v1/banks/{bank}/recall` | semantic + keyword + temporal recall |
| `POST` | `/v1/banks/{bank}/reflect` | broad synthesis bundle (curated tiers first) |
| `GET`  | `/v1/banks/{bank}/memories` | browse (paginated; `?type=&tag=`) |
| `DELETE` | `/v1/banks/{bank}/memories/{id}` | delete one |
| `GET` / `PUT` / `DELETE` | `/v1/banks[/{bank}]` | list / upsert / delete banks |

Memories are organized into **banks** (e.g. one per project or user) and can carry **tags** for
fine-grained recall filtering.

## Security

The whole database is encrypted at rest with **AES-256-GCM** under an **argon2id**
password-derived key (a KEK wrapping a random data key). No plaintext DB — or temp file — ever
touches the drive; the live database lives only in RAM while unlocked. The API binds to loopback
and validates the `Host` header. Full threat model in **[SECURITY.md](SECURITY.md)**.

> ⚠️ **No password recovery.** A forgotten password means the memories are unrecoverable, by
> design. This is early software — keep a backup of anything you can't lose.

## Running from a USB drive

`make dist` produces the layout you copy onto the drive — six platform binaries and three
launchers at the root, with all data kept separately in `mykeep_kb/`:

```
<DRIVE>/
├── mykeep.command  mykeep.cmd  mykeep.sh   ← double-click; auto-picks your binary
├── mykeep-darwin-amd64    mykeep-darwin-arm64
├── mykeep-linux-amd64     mykeep-linux-arm64
├── mykeep-windows-amd64.exe   mykeep-windows-arm64.exe
└── mykeep_kb/        ← all data: mykeep.db.enc, config, models/ (created on first launch)
```

Regenerable binaries stay cleanly separated from your data (`mykeep_kb`). Every binary resolves
`mykeep_kb/` as a sibling of itself, so the same drive works on any OS.

- **Format the drive as exFAT** — the only format read/write on Windows, macOS, and Linux out of
  the box.
- **Safe-eject** before unplugging. Memories re-seal a few seconds after each write and on clean
  shutdown; a hard yank loses at most the last few seconds.
- **Launch via the launcher** (`.command` / `.cmd` / `.sh`) — exFAT has no exec bit, so it runs
  the raw binary for you. Unsigned binaries: macOS `xattr -dr com.apple.quarantine <path>` or
  right-click → Open; Windows SmartScreen → More info → Run anyway.
- **One OS only?** Ship just that one `mykeep-<os>-<arch>` binary + its launcher (~16 MB vs
  ~100 MB for all six).

## Where it fits

Capsule is one of four mykeep components — all on one drive, under one password:

| | Component | Your agent can… |
|---|---|---|
| 🧠 | **Capsule** (this repo) | **know** you — encrypted, portable memory |
| 🔐 | **[Vault](https://github.com/lexxx233/mykeep-vault)** | **act as** you — a secrets broker that acts by reference |
| 🔮 | **[Showstone](https://github.com/lexxx233/mykeep-showstone)** | **see** the web — a contained browser it drives over REST |
| 🧰 | **[Foundry](https://github.com/lexxx233/mykeep-foundry)** | **do** more — sandboxed tools + the backend they run on |

## Development

```sh
make build      # local binary
make test       # go test ./...   (~123 tests)
make vet
make guard      # prove the build pulls in zero CGo
make cross      # build all six OS/arch targets, CGO_ENABLED=0
make dist       # assemble the USB drive layout (binaries + launchers)
```

Run a single test: `go test ./internal/retrieval -run TestRRF -v`. Requires **Go 1.26+**. The
whole stack is pure Go, so it cross-compiles to win/mac/linux × amd64/arm64 from one host. See
**[IMPLEMENTATION.md](IMPLEMENTATION.md)** for done-vs-deferred status.

## License

[MIT](LICENSE) © 2026 Domu Inc.

---

<div align="center">
<sub>Local-first AI agent memory · encrypted · portable · no cloud · no keys · built in Go.</sub>
</div>
