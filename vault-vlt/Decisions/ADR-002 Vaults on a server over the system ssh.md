---
title: "ADR-002: Vaults on a server over the system ssh"
tags:
  - decision
  - store
status: accepted
date: 2026-10-29
supersedes:
aliases:
  - ADR-002
---

# ADR-002: Vaults on a server over the system ssh

## Status
**Accepted** (2026-10-29) and implemented by [[F24 Storage abstraction]], [[F25 SFTP backend and server locations]], [[F26 Vault lock file]] and [[F27 Server vaults in the UI]].

## Context
The user wants a vault to live optionally on an SSH server, for example `10.20.20.150:/home/jas/vault`, instead of only on local disk. vlt encrypts secrets and the vault key on the client, so the server holds ciphertext (plus the plain-text `vlt.json` format marker), as a cloud-synced folder would. The open question was how vlt reaches the server.

## Options
| Option | Pros | Cons |
|---|---|---|
| **A. System `ssh` + SFTP over its pipes** | Uses `~/.ssh/config`, keys, the agent, `known_hosts`, `ProxyJump` for free. vlt never handles SSH credentials. One dependency (`pkg/sftp`) | Needs an `ssh` binary. vlt cannot answer prompts, so batch mode only |
| B. Pure Go SSH (`x/crypto/ssh`) | No external binary | Re-implements config parsing, agent, `known_hosts` and host-key prompts |
| C. No code: sshfs, Syncthing, rsync | Nothing to build | sshfs on macOS needs macFUSE; sync tools bring conflicts |

## Decision
**Option A.** vlt runs `ssh -o BatchMode=yes … -s -- host sftp` and speaks SFTP with `github.com/pkg/sftp`.

Two more decisions came with it:
- **No offline access.** If the server is down, the vault can't be opened. A local cache would bring stale and conflicting copies.
- **An advisory lock file**, `vlt.lock`, that warns when the same vault is open twice. Edits to the same secret are last-writer-wins.

## Consequences
- **Storage abstraction.** The store goes through a `vaultFS` interface, with vault-relative slash paths. The local and SFTP backends keep the same guarantees:
  - an atomic replace, via `posix-rename@openssh.com`;
  - an exclusive create, via `hardlink@openssh.com`;
  - modes 0600 and 0700;
  - fsync where the server offers `fsync@openssh.com`.
- **Unsupported servers.** A server without the two required extensions is refused.
- **Trust.** The server is trusted for availability and integrity. For confidentiality it is trusted no more than any holder of a copy of the vault: it can guess passphrases offline against `vlt.age`, so the passphrase is the protection. It can delete files or roll any file back undetected, and sees names, sizes and times. See [[Security Model]].
- **Host keys.** In batch mode, unknown hosts and password logins fail with ssh's own message, unless the user's ssh config sets `StrictHostKeyChecking accept-new` or `no` (vlt passes no host-key options). The user connects once from a terminal first.

## Related
- [[ADR-001 Scrypt-wrapped X25519 identity]]
- [[Storage Format]]
- [[Roadmap]]
