# Security

## Threat model

mykeep protects your memories against a **lost or stolen USB stick**: an attacker with
the binary and all data files should learn nothing about your stored memories without
your password.

A secondary consideration is the **local host**: while mykeep is running and unlocked,
the decryption key is in RAM and the REST API is reachable on loopback. mykeep trusts
other processes running as the same OS user (this is inherent — see below).

## What is encrypted

**Everything.** The entire SQLite database — memory text, the full-text (FTS5) keyword
index, entity names, and the embedding vectors — is encrypted as a single blob,
`mykeep.db.enc`, with **AES-256-GCM**. No plaintext database, and no plaintext temporary
file, is ever written to the stick: the live database lives only in RAM while unlocked and
is decrypted/re-sealed via SQLite's in-memory serialize/deserialize.

There is **no API key** to protect, because mykeep runs no LLM and makes no network
calls — so your password's only job is to encrypt your memories.

## Key management

- Your password is run through **argon2id** (memory ≈ 256 MiB, time = 4, threads pinned)
  to derive a key-encryption-key (KEK). All KDF parameters and a random 16-byte salt are
  stored in plaintext (they must be, to re-derive the key) — they are not secret.
- The KEK wraps a random 32-byte **data-encryption-key (DEK)**; the DEK seals the database
  blob. A password change would only re-wrap the DEK, never re-encrypt all data.
- The KDF parameters + salt are bound as AES-GCM **additional authenticated data**, so
  tampering with them is detected as an authentication failure.
- A **wrong password** fails the GCM authentication tag and is reported as such; the server
  does not start. There is **no recovery path** — a forgotten password means the memories
  are unrecoverable by design.
- The password is handled as a `[]byte` (never an immutable string) and is never logged;
  the DEK is zeroized on shutdown.

## Durability vs. a hard yank

Memories are re-sealed to the stick a few seconds after each write (debounced) and
synchronously on clean shutdown / safe-eject. A **hard power-loss or unplugging without
ejecting** can lose at most the last few seconds of writes; a clean eject loses nothing.

## Network exposure

The REST API binds to `127.0.0.1` and rejects non-loopback connections (validating both
the socket address and the HTTP `Host` header to blunt DNS-rebinding). An optional
`require_token` mode adds a bearer token (constant-time compared) for defense against
other local processes; it is off by default to keep the copy-paste integration snippet
simple. **An unlocked instance trusts every process of the same OS user** — this is
inherent to holding a decryption key in RAM and cannot be fully mitigated on commodity OSes.

## On exFAT/FAT USB media

Unix file permissions are ignored by exFAT/FAT, so on a stick the **encryption — not file
permissions — is the real protection**. This is exactly why the whole database is
encrypted rather than relying on access control.

## Reporting

This is pre-release software. Please open an issue for security concerns.
