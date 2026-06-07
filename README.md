<!--
GitHub topics to set in the repo's "About" sidebar (boosts discoverability):
ai-memory, agent-memory, llm-memory, local-first, privacy, encryption, vector-search,
semantic-search, embeddings, rag, claude, cursor, self-hosted, portable, golang, no-cgo
-->

<div align="center">

# 🧠 joyvend

### Portable, encrypted, local-first memory for AI agents — on a USB stick.

A single pure-Go binary + one encrypted file. Plug it into any machine, type your password,
and your AI assistant gains a **persistent, private, semantic memory** it talks to over a tiny
local API. No cloud. No API keys. No database to run.

![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)
![pure Go · no CGo](https://img.shields.io/badge/pure%20Go-no%20CGo-00ADD8)
![platforms](https://img.shields.io/badge/platforms-Windows%20%7C%20macOS%20%7C%20Linux-555)
![storage](https://img.shields.io/badge/at%20rest-AES--256--GCM%20encrypted-2ea043)
![local-first](https://img.shields.io/badge/local--first-no%20cloud%20%C2%B7%20no%20keys-1f6feb)
![status](https://img.shields.io/badge/status-pre--release-orange)

</div>

---

joyvend is a **self-contained memory server for LLM agents** (Claude Code, Cursor, or anything
with a shell/fetch tool). It stores what your agent learns, searches it semantically, and
encrypts everything at rest — all on the stick, with **zero host dependencies and no CGo**, so
one binary cross-compiles to Windows, macOS, and Linux. The agent does the reasoning; joyvend
just remembers.

> Think of it as a private, encrypted, USB-portable alternative to cloud "memory" features and
> heavyweight RAG stacks — without sending your data anywhere or running a database.

## ✨ Features

- 🔒 **Encrypted at rest** — the *entire* database (content, search index, and vectors) is one
  AES-256-GCM blob, sealed with an argon2id key derived from your password. A lost stick yields
  only ciphertext. There is no recovery by design.
- 🧲 **Semantic + keyword + temporal recall** — local CPU embeddings (`bge-small` via
  [cybertron](https://github.com/nlpodyssey/cybertron), vec0 KNN) + BM25 full-text + a date-aware
  arm, fused with Reciprocal Rank Fusion and a recency boost.
- 💻 **Runs anywhere, depends on nothing** — one static binary, no installer, no host services,
  **no CGo**. Cross-compiles to win/mac/linux × amd64/arm64 from a single host.
- 🪟 **Cross-platform GUI** — launches a local web app in your browser (no GUI toolkit, still one
  binary). Or use the terminal, the REST API, or the CLI.
- 🤖 **No LLM, no keys** — joyvend stores and retrieves; *your* agent does all the thinking. It
  never calls out to the cloud and has no API key to leak.
- 🧳 **Truly portable** — config and data live next to the binary on the stick, so moving the
  stick carries your whole memory with it.

## 🧩 How it works

```
 your AI agent  ──HTTP──▶  joyvend (local, loopback)  ──▶  encrypted SQLite on the USB stick
 (Claude Code,            retain / recall / search          AES-256-GCM · FTS5 · vec0 vectors
  Cursor, …)              local CPU embeddings               decrypted into RAM while unlocked
```

The agent already *is* the LLM, so joyvend deliberately runs no model of its own (except the
small local embedder used for search). You connect it by pasting a one-paragraph snippet into
your assistant — no MCP server, no plugin, no config files.

## 🚀 Quick start

```sh
go build ./cmd/joyvend      # or: make build  ->  bin/joyvend
./bin/joyvend               # opens the GUI in your browser (double-click on a stick)
```

On **first launch** you create a password; on every launch after, you're prompted for it (the DB
is decrypted into RAM, then served). Then paste this block — printed on launch, or via
`joyvend snippet` — into your AI assistant:

```
You have a persistent local memory (joyvend) at http://127.0.0.1:8765.
▶ First, fetch your instructions:  GET http://127.0.0.1:8765/v1/guide
Then follow them — remember facts about the user/project as you learn them, and
recall before you answer. Use your shell or fetch tool to call the API.
```

The agent fetches its full operating manual from `/v1/guide` (the retain / recall /
reflect / supersede protocol), then just `curl`s the local API. That's the whole
integration — no MCP, no plugin, no config. (For chat clients that can't fetch, the GUI's
**"Copy full instructions"** button and `joyvend guide` print the manual inline.)

## 🪟 The GUI

Running joyvend with **no arguments** (double-clicking the drive launcher) opens a local web app
in your default browser — served by joyvend itself, pure Go, no toolkit. It prompts for the
password, unlocks the store, and shows a dashboard to copy the agent snippet, add a memory, and
search.

<!-- Add a screenshot here once captured: ![joyvend GUI](docs/screenshot.png) -->

## 🛠 CLI

```sh
joyvend                 # default: open the GUI
joyvend serve           # terminal mode (prompts for the password; great over SSH)
joyvend snippet         # reprint the paste-into-your-agent block
joyvend guide           # print the full agent operating manual (also GET /v1/guide)
joyvend doctor          # diagnostics (no password needed)
joyvend retain "..."    # add a memory          (talks to a running server)
joyvend recall "..."    # search your memories
joyvend memories        # browse
joyvend banks           # list memory banks
joyvend version
```

Headless: set `JOYVEND_PASSPHRASE` (or pipe it on stdin) for `serve`.

## 🔌 HTTP API (loopback only)

| Method | Path | Purpose |
|---|---|---|
| `GET`  | `/v1/health` | status, embedder, memory count |
| `POST` | `/v1/banks/{bank}/retain` | store memories |
| `POST` | `/v1/banks/{bank}/recall` | semantic + keyword + temporal recall |
| `GET`  | `/v1/banks/{bank}/memories` | browse (paginated) |
| `DELETE` | `/v1/banks/{bank}/memories/{id}` | delete one |
| `GET` / `PUT` / `DELETE` | `/v1/banks[/{bank}]` | list / upsert / delete banks |

Memories are organized into **banks** (e.g. one per project or user) and can carry **tags** for
fine-grained recall filtering.

## 🔐 Security

The whole database is encrypted at rest with **AES-256-GCM** under an **argon2id** password-derived
key (KEK wrapping a random data key). No plaintext DB — or temp file — ever touches the stick; the
live database lives only in RAM while unlocked. The API binds to loopback and validates the `Host`
header. See **[SECURITY.md](SECURITY.md)** for the full threat model.

> ⚠️ Pre-release software. There is **no password recovery** — a forgotten password means the
> memories are unrecoverable, by design.

## 🧳 Running from a USB stick

`make dist` produces the drive layout you copy onto the stick — six platform binaries and three
launchers at the root, with all data kept separately in `joyvend_kb/`:

```
<DRIVE>/
├── joyvend.command  joyvend.cmd  joyvend.sh   ← double-click; auto-picks your binary
├── joyvend-darwin-amd64    joyvend-darwin-arm64
├── joyvend-linux-amd64     joyvend-linux-arm64
├── joyvend-windows-amd64.exe   joyvend-windows-arm64.exe
└── joyvend_kb/        ← all data: joyvend.db.enc, config, models/ (created on first launch)
```

The code (regenerable binaries) is cleanly separated from your data (`joyvend_kb`, the encrypted
knowledge base). Every binary resolves `joyvend_kb/` as a sibling of itself, so the same stick works
on any OS. Tips:

- **Format the stick as exFAT** — the only format read/write on Windows, macOS, and Linux out of
  the box.
- **Safe-eject** before unplugging. Memories re-seal a few seconds after each write and on clean
  shutdown; a hard yank loses at most the last few seconds.
- Launch via `joyvend.command` / `.cmd` / `.sh` (exFAT has no exec bit, so the launcher runs the raw
  binary for you). Unsigned binaries: macOS `xattr -dr com.apple.quarantine <path>` or right-click →
  Open; Windows SmartScreen → More info → Run anyway.
- Only ever use one OS? Ship just that one `joyvend-<os>-<arch>` binary + its launcher (~16 MB vs
  ~100 MB for all six).

## 🗺 Roadmap

Implemented and tested today: encrypted store, local CPU embeddings, vec0 + brute-force vector
search, keyword + semantic + temporal recall, migration framework, single-instance lock, the GUI,
REST API, and CLI. Still planned: runtime settings changes (`PATCH /settings`), extra key-in-RAM
hardening (mlock, idle auto-lock), and DEK rotation. See
**[IMPLEMENTATION.md](IMPLEMENTATION.md)** for the full status.

## 🧪 Development

```sh
make build      # local binary
make test       # go test ./...   (~108 tests)
make vet
make guard      # prove the build pulls in zero CGo
make cross      # build all six OS/arch targets, CGO_ENABLED=0
make dist       # assemble the USB drive layout (binaries + launchers)
```

Run a single test: `go test ./internal/retrieval -run TestRRF -v`. Requires **Go 1.26+**. The
whole stack is pure Go (no CGo), so it cross-compiles to win/mac/linux × amd64/arm64 from one host.

---

<div align="center">
<sub>Local-first AI agent memory · encrypted · portable · no cloud · no keys · built in Go.</sub>
</div>
