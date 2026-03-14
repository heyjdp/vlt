# vlt — Age-based Secret Manager
## Design Document v0.1

---

## Overview

`vlt` is a cross-platform GUI secret manager using [age](https://age-encryption.org/) encryption. It replaces ad-hoc PGP-encrypted text file workflows with a structured, portable vault based on a directory of per-secret `.age` files, unlocked by a single passphrase.

Target platforms: **Linux, macOS, Windows, OpenBSD**

---

## Identity & Unlock

- **Mechanism:** Passphrase-only, using age's scrypt recipient (`age.NewScryptRecipient`)
- The passphrase is used to derive an age identity on-the-fly at unlock
- **No key file is ever written to disk**
- The derived identity lives only in process memory and is zeroed on lock or session timeout
- The vault directory is fully portable — copy it anywhere and unlock with the same passphrase

---

## Directory Layout

The vault is a directory of `.age` files organised by secret type. The default vault location follows OS conventions:

| OS      | Default Path                              |
|---------|-------------------------------------------|
| Linux   | `~/.local/share/vlt/vault/`               |
| macOS   | `~/Library/Application Support/vlt/vault/`|
| Windows | `%APPDATA%\vlt\vault\`                    |
| OpenBSD | `~/.local/share/vlt/vault/`               |

### Vault Structure

```
vault/
├── passwords/
│   ├── github-api-key.age
│   └── aws-root-creds.age
├── records/
│   ├── personal-email.age
│   └── work-jira.age
└── files/
    ├── server-id_ed25519.age
    └── wildcard-cert.age
```

- Categories are plain subdirectories — no database, no index file
- Filenames are human-readable and correspond to the secret name (slugified)
- The structure is trivially Git-compatible

---

## Secret Envelope Format

Every `.age` file decrypts to a **JSON envelope**. All four secret types share this structure, differentiated by the `type` field.

### Common Fields

```json
{
  "version": 1,
  "type": "<password|record|file|key>",
  "name": "Human-readable name",
  "created_at": "2026-03-14T10:00:00Z",
  "modified_at": "2026-03-14T10:00:00Z",
  "tags": ["tag1", "tag2"],
  "notes": "Optional free-text notes"
}
```

### Type: `password`

For passwords, API keys, tokens, and other single-value text secrets.

```json
{
  "version": 1,
  "type": "password",
  "name": "GitHub API Key",
  "created_at": "2026-03-14T10:00:00Z",
  "modified_at": "2026-03-14T10:00:00Z",
  "tags": ["github", "ci"],
  "notes": "Expires 2027-03-14",
  "value": "ghp_xxxxxxxxxxxx"
}
```

### Type: `record`

For structured login records with URL, username, password, and optional TOTP.

```json
{
  "version": 1,
  "type": "record",
  "name": "Work Jira",
  "created_at": "2026-03-14T10:00:00Z",
  "modified_at": "2026-03-14T10:00:00Z",
  "tags": ["work"],
  "notes": "",
  "username": "jas@safenav.io",
  "password": "xxx",
  "url": "https://safenav.atlassian.net",
  "totp_secret": ""
}
```

### Type: `file`

For arbitrary binary files, SSH private keys, TLS certificates, etc. File content is base64-encoded inside the JSON envelope. Size limit: **10 MB** per file secret.

```json
{
  "version": 1,
  "type": "file",
  "name": "Server ED25519 Key",
  "created_at": "2026-03-14T10:00:00Z",
  "modified_at": "2026-03-14T10:00:00Z",
  "tags": ["ssh", "server"],
  "notes": "",
  "original_filename": "id_ed25519",
  "mime_type": "application/octet-stream",
  "encoding": "base64",
  "value": "LS0tLS1CRUdJTi..."
}
```

SSH keys and TLS certificates use the same `file` type. No tar-in-age complexity is required as files are small enough for base64-in-JSON.

---

## GUI Framework

**Choice: Go + Fyne**

| Criterion             | Go + Fyne                        | Python + PySide6                  |
|-----------------------|----------------------------------|-----------------------------------|
| Age library           | Native (`filippo.io/age`)        | `pyrage` wrapper                  |
| OpenBSD support       | ✅ Go + Fyne available via ports | ⚠️ Qt6 ports incomplete           |
| Distribution          | Single static binary             | PyInstaller or bundled runtime    |
| Cross-compilation     | `GOOS=windows go build`          | Complex                           |
| Secure memory         | `memguard` or manual zeroing     | Harder to guarantee               |

### Key Dependencies

```
filippo.io/age          — encryption/decryption
fyne.io/fyne/v2         — cross-platform GUI
github.com/awnumar/memguard — secure in-memory identity storage
```

---

## Application Architecture

```
vlt/
├── cmd/vlt/
│   └── main.go              # Entry point, Fyne app bootstrap
├── internal/
│   ├── crypto/
│   │   ├── identity.go      # Passphrase → age identity, memguard zeroing
│   │   └── vault.go         # Encrypt/decrypt envelope helpers
│   ├── store/
│   │   ├── store.go         # Directory walker, secret CRUD
│   │   └── secret.go        # Secret types and JSON envelope schema
│   └── ui/
│       ├── app.go           # Fyne app and window bootstrap
│       ├── unlock.go        # Passphrase entry screen
│       ├── list.go          # Secret browser (left panel)
│       ├── detail.go        # Secret view/edit (right panel)
│       └── settings.go      # Vault path, lock timeout, clipboard TTL
└── go.mod
```

### Package Responsibilities

**`internal/crypto`**
- `identity.go` — derives an `age.ScryptIdentity` from a passphrase; stores it using `memguard.LockedBuffer`; exposes a `Zero()` method to wipe on lock
- `vault.go` — `Encrypt(identity, envelope) ([]byte, error)` and `Decrypt(identity, ciphertext) ([]byte, error)` wrappers around `filippo.io/age`

**`internal/store`**
- `secret.go` — defines `Secret`, `PasswordSecret`, `RecordSecret`, `FileSecret` structs; JSON marshalling/unmarshalling; filename slugification
- `store.go` — `List()`, `Get(name)`, `Save(secret)`, `Delete(name)`, `Move(secret, category)` operating on the vault directory; decrypts on `Get`, encrypts on `Save`

**`internal/ui`**
- `unlock.go` — passphrase entry dialog; calls `crypto.identity` on confirm; transitions to main view
- `list.go` — category tree + secret list; filters by search query; fires selection events
- `detail.go` — renders the correct form for the secret type; handles clipboard copy with TTL countdown
- `settings.go` — vault path picker, auto-lock timeout slider, clipboard TTL slider
- `app.go` — wires everything together; manages lock/unlock state; runs the inactivity timer

---

## UX Features

### Clipboard Safety
- Copying any secret value clears the clipboard after a configurable TTL (default: **30 seconds**)
- A countdown is shown in the status bar while a value is in the clipboard
- Implemented via a goroutine that writes an empty string to the clipboard on expiry

### Session Lock
- Auto-lock after N minutes of inactivity (default: **5 minutes**)
- On lock: the `memguard.LockedBuffer` holding the derived identity is zeroed and destroyed
- Passphrase must be re-entered to resume; no in-memory state is preserved across lock cycles
- The inactivity timer resets on any UI interaction event

### Search
- Instant filter across all decrypted secret names and tags within the current unlocked session
- File content and notes are not searched
- Implemented client-side on the in-memory secret list — no disk reads during search

### File Import / Export
- **Import:** file picker → read → base64 encode → wrap in `file` envelope → encrypt → save
- **Export:** decrypt secret → base64 decode → write to user-selected path via file picker
- MIME type is detected at import time using Go's `net/http.DetectContentType`
- Hard size limit of **10 MB** enforced at import

### Git Integration (Optional)
- On startup, detect if the vault directory is inside a git repository (`git rev-parse`)
- If detected, offer a toggle in Settings: "Commit after each write"
- When enabled, `vlt` runs `git add <file> && git commit -m "vlt: update <secret-name>"` after every save/delete
- This is not a hard dependency; the app functions fully without git

---

## OpenBSD Notes

- Fyne requires OpenGL — install `mesa-dri` via `pkg_add` before running
- Build with standard `go build`; no CGO required for core functionality
- `pledge(2)` / `unveil(2)` integration is deferred to a later phase due to Fyne's use of internal threads and OpenGL context
- No system keychain exists on OpenBSD — the passphrase-only model is the correct choice and requires no OS-specific keychain integration on any platform

---

## Build & Distribution

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o vlt ./cmd/vlt

# macOS (arm64)
GOOS=darwin GOARCH=arm64 go build -o vlt ./cmd/vlt

# Windows
GOOS=windows GOARCH=amd64 go build -o vlt.exe ./cmd/vlt

# OpenBSD
GOOS=openbsd GOARCH=amd64 go build -o vlt ./cmd/vlt
```

For macOS, wrap with `fyne package` to produce a proper `.app` bundle. For Windows, `fyne package` produces an `.exe` with embedded icon.

---

## Development Phases

### Phase 1 — Core (`crypto` + `store`, no GUI)
- [ ] `go.mod` with `filippo.io/age`, `memguard`
- [ ] `crypto/identity.go` — passphrase → identity, zero on lock
- [ ] `crypto/vault.go` — encrypt/decrypt helpers
- [ ] `store/secret.go` — envelope types, JSON schema
- [ ] `store/store.go` — CRUD on vault directory
- [ ] CLI smoke test: encrypt a secret, list it, decrypt it

### Phase 2 — GUI Skeleton
- [ ] Fyne app bootstrap
- [ ] Unlock screen with passphrase entry
- [ ] Secret list panel (left)
- [ ] Detail view for `password` and `record` types (right)
- [ ] Basic add/edit/delete flow

### Phase 3 — File Secrets
- [ ] File import via file picker
- [ ] File export to disk
- [ ] MIME detection
- [ ] 10 MB size limit enforcement

### Phase 4 — Polish
- [ ] Clipboard TTL with status bar countdown
- [ ] Auto-lock with inactivity timer
- [ ] Search / filter
- [ ] Settings screen (vault path, lock timeout, clipboard TTL)
- [ ] Optional git commit on write

---

## Security Considerations

- The passphrase is never stored anywhere — not in memory beyond the scrypt derivation call, not on disk
- The derived age identity is held in a `memguard.LockedBuffer` (mlock'd, guard pages) and explicitly zeroed on lock
- Secret plaintext exists in memory only during the active session and is not cached between operations
- The vault directory should be on a local filesystem — storing it on a network share or cloud-synced folder is the user's responsibility
- File permissions on the vault directory should be set to `0700` on creation (Unix); `vlt` enforces this on startup and warns if they have been widened

