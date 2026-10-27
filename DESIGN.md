# vlt — Age-based Secret Manager
## Design Document v0.1

---

## Overview

`vlt` is a cross-platform GUI secret manager using [age](https://age-encryption.org/) encryption. It replaces ad-hoc PGP-encrypted text file workflows with a structured, portable vault based on a directory of per-secret `.age` files, unlocked by a single passphrase.

Target platforms: **Linux, macOS, Windows, OpenBSD**

---

## Identity & Unlock

- **Mechanism:** a random age X25519 identity (the *vault key*) encrypts every secret; the vault key is stored in `vlt.age`, encrypted to the passphrase with age's scrypt recipient (`age.NewScryptRecipient`). See ADR-001 in the project vault (`vault-vlt/Decisions/`).
- Unlock runs exactly one scrypt derivation (decrypting `vlt.age`); secret decryption is X25519 and costs microseconds
- No key material leaves the vault directory, and the only key on disk is passphrase-encrypted
- The vault key lives in process memory (memguard) and is zeroed on lock or session timeout
- Changing the passphrase atomically rewrites only `vlt.age`
- `vlt.json` records the format version (2); format-1 vaults (secrets encrypted directly to the passphrase) are migrated on first unlock, crash-safely
- The vault directory is fully portable — copy it anywhere and unlock with the same passphrase

---

## Directory Layout

The vault is a directory of `.age` files organised by group. The default vault location follows OS conventions:

| OS      | Default Path                              |
|---------|-------------------------------------------|
| Linux   | `$XDG_DATA_HOME/vlt/vault/` (default `~/.local/share/vlt/vault/`) |
| macOS   | `~/Library/Application Support/vlt/vault/`|
| Windows | `%APPDATA%\vlt\vault\`                    |
| OpenBSD | `$XDG_DATA_HOME/vlt/vault/` (default `~/.local/share/vlt/vault/`) |

On Linux and OpenBSD a relative `XDG_DATA_HOME` is ignored, and a vault already in `~/.local/share/vlt/vault/` is kept when the XDG location holds none.

### Vault Structure

```
vault/
├── vlt.age
├── vlt.json
├── default/
│   └── wifi.age
├── personal-finance/
│   └── bank.age
└── work/
    ├── github-api-key.age
    ├── work-jira.age
    └── server-id-ed25519.age
```

- Groups are plain subdirectories named by the slugified group name; the envelope keeps the name as typed — no database, no index file
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
  "group": "Group name as typed",
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

For arbitrary binary files, SSH private keys, TLS certificates, etc. File content is base64-encoded inside the JSON envelope. Size limit: **10 MiB** per file secret.

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
| Distribution          | Single binary (links system OpenGL) | PyInstaller or bundled runtime |
| Cross-compilation     | Native build per OS (cgo); `fyne-cross` for releases | Complex             |
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
│   │   ├── identity.go      # X25519 vault key (memguard); wrap/unwrap with passphrase
│   │   └── vault.go         # Encrypt/decrypt to the vault key
│   ├── store/
│   │   ├── store.go         # Directory walker, secret CRUD, copy
│   │   ├── vault.go         # Create/Open/ChangePassphrase, format marker
│   │   ├── migrate.go       # Crash-safe format-1 → format-2 migration
│   │   ├── atomic.go        # Atomic file writes
│   │   └── secret.go        # Secret types and JSON envelope schema
│   └── ui/
│       ├── app.go           # Fyne app and window bootstrap
│       ├── unlock.go        # Passphrase entry screen (existing vault)
│       ├── create.go        # Create-vault screen (passphrase + confirmation, open existing)
│       ├── runner.go        # Background execution of vault I/O
│       ├── activity.go      # Input tracking for auto-lock
│       ├── clipboard.go     # Clipboard copy with auto-clear
│       ├── config.go        # Settings load/clamp/save (Fyne preferences)
│       ├── list.go          # Secret browser (left panel)
│       ├── detail.go        # Secret view/edit (right panel)
│       └── settings.go      # Vault path, lock timeout, clipboard TTL
└── go.mod
```

### Package Responsibilities

**`internal/crypto`**
- `identity.go` — generates the X25519 vault key and holds it in a `memguard.LockedBuffer`; `WrapIdentity`/`UnwrapIdentity` encrypt it to and from the passphrase (scrypt); `DecryptWithPassphrase` reads legacy files; `Zero()` wipes on lock
- `vault.go` — `Encrypt(identity, envelope) ([]byte, error)` and `Decrypt(identity, ciphertext) ([]byte, error)` to the vault key

**`internal/store`**
- `secret.go` — defines `Secret`, `PasswordSecret`, `RecordSecret`, `FileSecret` structs; JSON marshalling/unmarshalling; filename slugification
- `store.go` — `List()`, `Get(name, group)`, `Create(secret)`, `Update(secret)`, `Delete(name, group)`, `CopyTo(dir)` operating on the vault directory. `Create` never overwrites: it fails with `ErrSecretExists` if the slugified path is taken. `Update` requires the stored secret to have exactly the same name
- `vault.go` — `Create`, `Open`, `Exists`, `ChangePassphrase`, `Close`; reads and writes `vlt.age` (wrapped vault key) and `vlt.json` (format marker)
- `migrate.go` — in-place, resumable migration of format-1 vaults on first unlock
- `atomic.go` — temp-file + fsync + rename writes used for every vault file

**`internal/ui`**
- `unlock.go` — passphrase entry for an existing vault; `App` calls `store.Open` and transitions to the main view
- `create.go` — shown instead of `unlock.go` when `store.Exists` is false: displays the target directory, takes the passphrase twice (at least 12 characters excluding surrounding spaces; five or more random words recommended), then `App` calls `store.Create`. If `store.Exists` returns an error (directory unreadable), an error screen with Retry is shown instead of either screen. Unless `VLT_VAULT_DIR` sets the location, it also offers *Open Existing Vault…*: a folder picker whose choice is checked with `store.Exists` and refused if it holds no vault; otherwise `App` switches to it and shows the unlock screen, saving the location after the next successful unlock
- `runner.go` — every blocking store call (unlock/create scrypt, the folder check for Open Existing Vault, list, save, delete, import, passphrase change, vault copy) runs off the Fyne main goroutine via `background`, which delivers the result with `fyne.Do`. Unlock and create show a progress bar with input disabled; save and passphrase change disable their button and show status text. After unlock, components share a runner bound to the session context: `Lock` cancels it, store calls receive it, and any result that arrives after the lock is dropped rather than shown or acted on. An unlock, create or open-existing result for a screen that has since been replaced is discarded (and any opened store closed). Saves, deletes and imports update the list row in place instead of re-listing (changes made during the first load are replayed onto it). Tests inject an inline or queued runner so app-level tests are deterministic
- `list.go` — secret list; filters by search query; fires selection events. `Upsert`/`Remove` update rows in place, keep the current filter, and keep the highlight on the selected secret (moving it if its row moves, clearing it if the secret is deleted or filtered out)
- `detail.go` — renders the correct form for the secret type; its copy buttons call the app's `clipboardClearer`
- `clipboard.go` — one `clipboardClearer` per app: per-copy countdown, clears only vlt's own value (SHA-256 check), cleared on lock
- `settings.go` — vault path picker, auto-lock on/off and timeout slider, clipboard TTL slider. Slider changes are saved when a drag ends; `App` saves the vault location after every successful unlock or create (unless `VLT_VAULT_DIR` sets it), so a new location from Settings or the create screen's *Open Existing Vault…* sticks only once a vault opens there, and the default location, which on Linux depends on `XDG_DATA_HOME`, stays the same across launches. Settings provided by environment variables are disabled and labelled
- `config.go` — `LoadConfig` resolves each setting from env var → saved preference → default and clamps it (auto-lock 30 s–30 min or off; clipboard TTL 5–120 s); `SaveVaultDir`/`SaveLockTimeout`/`SaveClipboardTTL` save one setting each, only when the user changes it, so environment values are never persisted
- `app.go` — wires everything together; manages lock/unlock state; runs the inactivity timer

---

## UX Features

### Clipboard Safety
- Copying any secret value clears the clipboard after a configurable TTL (default: **30 seconds**); the TTL is read at each copy, so a Settings change applies to the next copy
- A countdown is shown in the status bar while a value is in the clipboard
- Implemented by one `clipboardClearer` per app (`clipboard.go`), driven by one-second timers. Each copy starts a new countdown generation, so an earlier copy can never cut a later one short
- On expiry the clipboard is cleared only if it still holds the value vlt copied (compared by SHA-256, so no second plaintext copy is kept). Content copied from elsewhere in the meantime is left alone
- Locking the vault or quitting clears vlt's value immediately and stops the countdown (quit is intercepted on window close and via the Quit menu item, because the system clipboard outlives the process). An empty clipboard read, which Fyne returns when the OS clipboard is locked by another program, is treated as vlt's value and cleared

### Session Lock
- Auto-lock after N minutes of inactivity (default: **5 minutes**)
- On lock: the `memguard.LockedBuffer` holding the vault key is zeroed and destroyed (best-effort; see Security Considerations)
- Passphrase must be re-entered to resume; no in-memory state is preserved across lock cycles
- The inactivity timer resets on user input in the main view and detail/settings panel: typing in an entry, pressing a button, selecting a list row or moving a slider (`activity.go` wraps these widgets' callbacks, because Fyne has no global input hook), plus key presses when no widget has focus. Widgets inside dialogs are not instrumented; instead the dialogs' callbacks (file pickers, import form, confirmations) report activity when the user answers them. Pointer movement, scrolling and split dragging do not count
- With a timeout of at least 60 seconds, a dialog warns 30 seconds before locking: *Stay unlocked* (or any other input) restarts the countdown, *Lock now* locks immediately. The lock itself always happens if the user is away. Timer callbacks carry a generation number, so a timer replaced by fresh activity never warns or locks

### Search
- Instant filter across all decrypted secret names and tags within the current unlocked session
- File content and notes are not searched
- Implemented client-side on the in-memory secret list — no disk reads during search

### File Import / Export
- **Import:** file picker → read up to 10 MiB (in the background; any read error aborts the import, and files over the limit are rejected) → MIME detect, base64 encode, SHA-256 → choose group → wrap in `file` envelope → encrypt → save (`Create`, asking before replacing) → completion dialog shows the SHA-256
- **Export:** base64 decode (in the background) → choose path → write and close (in the background; a failed write or close is reported and the partial file deleted where possible). The decoded plaintext is wiped after writing, on cancel, on error, and if the vault locks first; an export confirmed after a lock writes nothing
- Locking dismisses every open dialog (Fyne draws dialogs, file pickers included, inside the window), so no dialog outlives the session
- MIME type is detected at import time using Go's `net/http.DetectContentType`

### Git Integration (Optional)
- On startup, detect if the vault directory is inside a git repository (`git rev-parse`)
- If detected, offer a toggle in Settings: "Commit after each write"
- When enabled, `vlt` runs `git add <file> && git commit -m "vlt: update <secret-name>"` after every save/delete
- This is not a hard dependency; the app functions fully without git

---

## OpenBSD Notes

- Fyne requires OpenGL — install `mesa-dri` via `pkg_add` before running
- Build on OpenBSD itself with `go build`: Fyne needs cgo, a C compiler and mesa, so it cannot be cross-compiled from another OS
- `pledge(2)` / `unveil(2)` integration is deferred to a later phase due to Fyne's use of internal threads and OpenGL context
- No system keychain exists on OpenBSD — the passphrase-only model is the correct choice and requires no OS-specific keychain integration on any platform

---

## Build & Distribution

Fyne needs cgo, so vlt is built natively on each target OS (CI uses Linux, macOS and Windows runners); `fyne-cross` builds several platforms from one machine using Docker.

```bash
go build -trimpath -ldflags="-s -w" -o vlt ./cmd/vlt
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
- [ ] 10 MiB size limit enforcement

### Phase 4 — Polish
- [x] Clipboard TTL with status bar countdown
- [x] Auto-lock with inactivity timer
- [ ] Search / filter
- [x] Settings screen (vault path, lock timeout, clipboard TTL)
- [ ] Optional git commit on write

---

## Security Considerations

- The passphrase is never stored on disk; on disk, only the passphrase-encrypted vault key (`vlt.age`) and secrets encrypted to it exist

**Memory protection is best-effort.** Go strings are immutable and cannot be wiped, the garbage collector may copy memory, and Fyne keeps widget text as strings. vlt therefore limits *how long* and *where* secrets are reachable rather than promising they are erased:

- The passphrase is read from the unlock (or change-passphrase) entry and the entry is cleared immediately, on every path (`SetText` also clears the entry's undo history). It is used once to unwrap `vlt.age` and is not kept for the session
- The vault key's encoded form is held in a `memguard.LockedBuffer` (mlock'd, guard pages) and destroyed on lock and exit. age's parsed `X25519Identity` is kept alongside it for the session (parsing once, rather than per operation, avoids a fresh heap copy of the key for every secret); it and one transient string copy remain on the Go heap until garbage-collected
- Decrypted secrets are held, as Go strings, by the secret list and the detail panel while the vault is unlocked. On lock, vlt empties every loaded secret struct and blanks the detail form's entries (Fyne's renderer cache can keep old widgets reachable for a minute or more after they leave the window, so emptying beats merely dropping), then drops its references to the list, detail panel, main view and activity tracker, dismisses open dialogs (wiping a pending export buffer), and clears the clipboard if it still holds the value vlt copied. The strings are reclaimed by the garbage collector, not overwritten; background work still in flight at the lock keeps its own copy until it completes and its result is dropped
- Decrypted JSON and file buffers (`[]byte`) that vlt owns are zeroed once parsed, encoded or written (including every secret read while listing the vault). Intermediate copies made inside `io.ReadAll`, age and `encoding/json` are not
- The vault directory should be on a local filesystem — storing it on a network share or cloud-synced folder is the user's responsibility
- All vault writes go through a temp-file + fsync + rename + directory-fsync helper, so a crash or full disk never leaves a truncated `.age` file (directory fsync is skipped on Windows). Atomicity is per file, so multi-file operations are ordered so any interrupted prefix leaves the vault readable: passphrase change rewrites only `vlt.age`, and format-1 migration writes the new key to a staging file before touching secrets and swaps `vlt.age` only after every secret is re-encrypted
- vlt creates vault directories with mode `0700` and files with `0600` (Unix). A directory that already existed keeps its mode, and vlt does not re-check or repair permissions later, so permissions widened by other tools stay widened

