---
title: Storage Format
tags:
  - architecture
  - store
created: 2026-10-27
---

# Storage Format

## Directory layout

```mermaid
graph TD
    root["vault/"] --> key["vlt.age<br/><i>Enc_scrypt(AGE-SECRET-KEY-1…)</i>"]
    root --> fmt["vlt.json<br/><i>{&quot;format&quot;:2}</i>"]
    root --> g1["default/"]
    root --> g2["work/"]
    g1 --> f1["github-key.age"]
    g2 --> f2["aws-prod.age"]
    g2 --> f3["id-rsa.age"]
```

- Default location: `~/Library/Application Support/vlt/vault` (macOS), `%APPDATA%\vlt\vault` (Windows), `~/.local/share/vlt/vault` (others). You can override it with `VLT_VAULT_DIR`.
- Permissions: directories `0700`, files `0600`.
- Path: `vault/<NormalizeGroup(group)>/<Slugify(name)>.age`.
- The envelope's `group` is the name as typed and trimmed (`GroupLabel`); only the directory is slugified. Labels that differ only in case or punctuation (`Personal Finance`, `personal-finance`) share one directory. The UI lists one label per directory and files new secrets under the existing spelling.
- Secrets are encrypted to the vault's X25519 key. `vlt.age` holds that key, encrypted to the passphrase (scrypt). `vlt.json` records the format version. `vlt.migrating.age` exists only while a format-1 vault is being migrated.

## Slug function

$$
\operatorname{slug}(s) = \operatorname{trim}_{\texttt{-}}\Big(\operatorname{replace}_{[^{a\text{-}z0\text{-}9}]^{+}\,\to\,\texttt{-}}\big(\operatorname{lower}(\operatorname{trim}(s))\big)\Big)
$$

`slug` is **not injective**. Many names map to the same file:

| Name | Slug |
|---|---|
| `GitHub Key` | `github-key` |
| `github-key` | `github-key` |
| `GitHub_Key!` | `github-key` |
| `日本` / `!!!` | *(empty)*: rejected by `NewSecretName` |

Because `slug` isn't injective, `Create` writes exclusively (temp file + `link`) and returns `ErrSecretExists` if the file exists. `Update` requires the stored name to match exactly. Slugs longer than 251 bytes are rejected, so `<slug>.age` fits in a 255-byte filename. See [[F03 Prevent silent overwrites]].

> [!tip] Safety property
> Because the path uses only the slugs of group and name, which only emits `[a-z0-9-]`, path traversal (`../`) is impossible. Keep that property in whatever replaces it.

## Secret envelope

Each `.age` file decrypts to an indented JSON document (`internal/store/secret.go`):

```jsonc
{
  "version": 1,
  "type": "password | record | file",
  "name": "GitHub Key",
  "group": "work",
  "created_at": "2026-10-27T09:00:00Z",
  "modified_at": "2026-10-27T09:00:00Z",
  "tags": ["dev"],
  "notes": "",
  "value": "...",            // password type; base64 body for file type
  "username": "", "password": "", "url": "", "totp_secret": "",
  "original_filename": "", "mime_type": "", "encoding": "base64"
}
```

File secrets are base64-encoded inside `value`, so a 10 MiB import becomes about $\tfrac{4}{3}\times 10 \approx 13.3$ MiB of JSON. `List()` decrypts **every** file, including large file secrets, just to show names. An unencrypted-name index or a separate encrypted metadata file would avoid that.

## Write semantics

Every write goes through `writeFileAtomic` (`internal/store/atomic.go`), added by [[F02 Atomic file writes]]:

```mermaid
flowchart LR
    T["CreateTemp<br/>.tmp-* in same dir"] --> W[write] --> C["chmod 0600"] --> S[fsync file] --> R["rename over target"] --> D["fsync dir<br/>(not on Windows)"]
```

- A crash leaves either the old file or the new one, never a truncated file.
- New group directories are fsynced into their parent (`mkdirAllSynced`).
- Leftover `.tmp-*` files never end in `.age`, so `List` and `CopyTo` skip them.
- Atomicity is **per file**. Since [[F01 Scrypt-wrapped identity]], passphrase change rewrites only `vlt.age`. Format-1 migration stages the new key in `vlt.migrating.age` (created exclusively), re-encrypts secrets one atomic write at a time, swaps `vlt.age` only after every secret is done, then writes `vlt.json`.

## Related
- [[Architecture Overview]]
- [[Unlock Flow]]
