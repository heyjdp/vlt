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
| Cross-compilation     | Native build per OS (cgo); release packages built natively in CI | Complex             |
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
│   │   ├── location.go      # Vault locations (local, host:path, ssh://)
│   │   ├── fs.go            # Storage interface; local file system
│   │   ├── sshfs.go         # SFTP storage over the system's ssh
│   │   ├── lock.go          # vlt.lock (who has the vault open)
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
│       ├── settings.go      # Vault path, lock timeout, clipboard TTL
│       ├── style.go         # Cards, headings, empty states, type icons
│       ├── theme.go         # Teal accent, light/dark palettes, text sizes
│       └── icon.png         # App icon (256px), embedded
├── packaging/
│   ├── macos/               # vlt.app bundler: Info.plist template, build-app.sh
│   └── linux/               # AppImage build script, desktop entry, 256px icon
├── Icon.png                 # App icon (1024×1024); the 256px copies in internal/ui and packaging/linux come from it (sips -z 256 256 Icon.png --out …)
└── go.mod
```

### Package Responsibilities

**`internal/crypto`**
- `identity.go` — generates the X25519 vault key and holds it in a `memguard.LockedBuffer`; `WrapIdentity`/`UnwrapIdentity` encrypt it to and from the passphrase (scrypt); `DecryptWithPassphrase` reads legacy files; `Zero()` wipes on lock
- `vault.go` — `Encrypt(identity, envelope) ([]byte, error)` and `Decrypt(identity, ciphertext) ([]byte, error)` to the vault key

**`internal/store`**
- `secret.go` — defines `Secret`, `PasswordSecret`, `RecordSecret`, `FileSecret` structs; JSON marshalling/unmarshalling; filename slugification
- `store.go` — `List()`, `Get(name, group)`, `Create(secret)`, `Update(secret)`, `Delete(name, group)`, `CopyTo(location)` operating on the vault location. `Create` never overwrites: it fails with `ErrSecretExists` if the slugified path is taken. `Update` requires the stored secret to have exactly the same name
- `vault.go` — `Create`, `Open`, `Exists`, `ChangePassphrase`, `Close`; reads and writes `vlt.age` (wrapped vault key) and `vlt.json` (format marker)
- `migrate.go` — in-place, resumable migration of format-1 vaults on first unlock
- `atomic.go` — temp-file + fsync + rename writes used for every local vault file
- `fs.go` — `vaultFS`, the storage every other store file goes through. Its names are slash-separated paths relative to the vault directory, so the store never builds OS- or server-specific paths. It promises atomic writes (old or new contents, never partial), exclusive creates (never replace), an atomic rename, modes 0600/0700, and names sorted as `os.ReadDir` sorts them. `localFS` implements it with `atomic.go`. Directory walks skip dot-folders (`isGroupDir`): group directories are slugs, so a dot-folder is never a group
- `location.go` — `ParseLocation` reads a local path, `[user@]host:path` or `ssh://[user@]host[:port]/path`, and `Location.String` writes the form it reads back. Host and user may hold only letters, digits, `.`, `_` and `-` (not first; IPv6 hosts also `:` and `%`): ssh can substitute them into commands from the user's ssh config (`%h`, `%u`), and OpenSSH before 9.6 does not reject shell characters itself
- `sshfs.go` — `remoteFS`, the SFTP implementation. `dialSSH` runs the system's `ssh -T -x -a -o BatchMode=yes -o ClearAllForwardings=yes -o PermitLocalCommand=no -o RemoteCommand=none … -s -- host sftp` (with `ConnectTimeout`, `ServerAlive*`, and `WaitDelay` so a stuck ProxyJump child cannot hold up `Wait`; on Windows without a console window) and speaks SFTP over its pipes with `github.com/pkg/sftp`. `~/.ssh/config`, keys, the agent and `known_hosts` all apply, and vlt never handles SSH credentials; it passes no host-key options, so a config that sets `StrictHostKeyChecking=accept-new` or `no` is honoured. When ssh fails, its stderr is shown. Writes keep the local guarantees: temp files are created in `.vlt-tmp`, a private folder (made 0700, or tightened if loosened, once per session; temp files over an hour old are removed), so no other user can open one in the moment before its own 0600 mode is set (SFTP creates files with the server's umask). A temp file is fsynced when the server offers `fsync@openssh.com`, then renamed with `posix-rename@openssh.com` (atomic replace) or hard-linked with `hardlink@openssh.com` (exclusive create; SFTP gives no reason for a failed link, so the target is checked to report `ErrExist`, and where the server's file system cannot link, an exclusive `O_EXCL` create is the fallback, as locally). A server without `posix-rename` and `hardlink` is refused (`ErrServerUnsupported`). Directories cannot be fsynced over SFTP, so a crash of the server can undo the most recent rename (leaving the previous version), never leave a partial file. Listings are sorted by name and drop names that aren't a single path element, so a hostile server cannot steer a copy out of the destination. A dropped connection fails the operation that saw it (`ErrConnectionLost`, never retried, since a write may have landed) and the next operation reconnects; each session records its own extensions. `Store.Close` zeroes the key at once and closes the connection in the background, so a dead network never holds up locking
- `lock.go` — `AcquireLock` creates `vlt.lock` exclusively with this host, user, PID, time and a random token (retrying if the lock disappears in between). A lock held elsewhere is returned as `*InUseError`, which the UI shows before asking whether to take it over. A lock from this host whose process is gone (a crash), or from this process, is taken over silently; hosts are told apart by name only, so two computers with the same host name count as one. `Close` releases the lock by renaming it aside atomically, then checking the token: a lock that turns out to be someone else's is put back unless a newer one has appeared. The lock is advisory and holds no secrets; failing to write it is logged and does not stop an unlock
- `List` reads up to eight secret files in parallel (keeping directory order), which hides the per-file round trip to a server. A file it cannot read or decrypt is logged and skipped, but a lost connection fails the whole listing rather than showing a partial one

**`internal/ui`**
- `unlock.go` — passphrase entry for an existing vault, naming the vault's location (a local path in the home folder as `~/…`); `App` calls `store.Open`, takes the vault's lock, and transitions to the main view
- `create.go` — shown instead of `unlock.go` when `store.Exists` is false: displays the target directory, takes the passphrase twice (at least 12 characters excluding surrounding spaces; five or more random words recommended), then `App` calls `store.Create`. If `store.Exists` returns an error (directory unreadable), an error screen with Retry is shown instead of either screen. Unless `VLT_VAULT_DIR` sets the location, it also offers *Open Existing Vault…*: a folder picker whose choice is checked with `store.Exists` and refused if it holds no vault; otherwise `App` switches to it and shows the unlock screen, saving the location after the next successful unlock. It also offers *Use a Vault on a Server…*: a "Vault on a Server" dialog whose entry must parse as a remote location; *Use This Location* switches to it, showing its unlock screen if a vault is there and the create screen for it if not. The error screen says "Cannot reach the vault's server" for server errors and, unless `VLT_VAULT_DIR` sets the location or it already is the default, offers *Use the Vault on This Computer*
- `runner.go` — every blocking store call (unlock/create scrypt, the folder check for Open Existing Vault, list, save, delete, import, passphrase change, vault copy) runs off the Fyne main goroutine via `background`, which delivers the result with `fyne.Do`. Unlock and create show a progress bar with input disabled; save and passphrase change disable their button and show status text. After unlock, components share a runner bound to the session context: `Lock` cancels it, store calls receive it, and any result that arrives after the lock is dropped rather than shown or acted on. An unlock, create or open-existing result for a screen that has since been replaced is discarded (and any opened store closed). Saves, deletes and imports update the list row in place instead of re-listing (changes made during the first load are replayed onto it). Tests inject an inline or queued runner so app-level tests are deterministic
- `list.go` — secret list; filters by search query; fires selection events. `Upsert`/`Remove` update rows in place, keep the current filter, and keep the highlight on the selected secret (moving it if its row moves, clearing it if the secret is deleted or filtered out)
- `detail.go` — renders the correct form for the secret type; its copy buttons call the app's `clipboardClearer`. The form sits in a vertical scroll so long records never clip
- `theme.go` / `style.go` — `VltTheme` sets a teal accent (the icon's) on neutral light and dark palettes, rounded inputs and text sizes; `style.go` builds every screen from plain containers (cards, headings, empty states), never wrapping widgets, so the activity tracker and the lock-time blanking of entries still reach every field. A theme test checks WCAG contrast for body text, secondary text (including read-only entries) and accent-button text in both variants
- `clipboard.go` — one `clipboardClearer` per app: per-copy countdown, clears only vlt's own value (SHA-256 check), cleared on lock
- `settings.go` — vault path picker, auto-lock on/off and timeout slider, clipboard TTL slider. Slider changes are saved when a drag ends; `App` saves the vault location after every successful unlock or create (unless `VLT_VAULT_DIR` sets it), so a new location from Settings or the create screen's *Open Existing Vault…* sticks only once a vault opens there, and the default location, which on Linux depends on `XDG_DATA_HOME`, stays the same across launches. Settings provided by environment variables are disabled and labelled
- `config.go` — `LoadConfig` resolves each setting from env var → saved preference → default and clamps it (auto-lock 30 s–30 min or off; clipboard TTL 5–120 s); `SaveVaultDir`/`SaveLockTimeout`/`SaveClipboardTTL` save one setting each, only when the user changes it, so environment values are never persisted. `Config.DefaultVaultDir` keeps the default local location for that offer
- `app.go` — wires everything together; manages lock/unlock state; runs the inactivity timer. After a vault opens or is created it takes the lock; if another copy of vlt holds it, the opened store waits (`App.pending`) behind a "Vault Already Open" dialog: *Open Anyway* takes the lock over, *Cancel* closes the store, and no answer within the auto-lock timeout closes it too. Quitting closes the open or waiting store and waits up to 3 seconds for a remote one to release its lock

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

Fyne needs cgo, so vlt is built natively on each target OS (CI uses Linux, macOS and Windows runners). CI also packages a macOS app on a macOS runner and a Linux AppImage on Ubuntu 22.04, and publishes both as a GitHub release for `v*` tags.

```bash
go build -trimpath -ldflags="-s -w" -o vlt ./cmd/vlt
```

`make package-mac` builds an ad-hoc signed `vlt.app` for macOS 13 or later with `packaging/macos/build-app.sh`, using only tools that ship with macOS. `make package-linux` builds an AppImage with `packaging/linux/build-appimage.sh`. Windows packages are not built yet.

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
- A vault on an SSH server is reached through the system's ssh client, so authentication, host-key checking and transport encryption are OpenSSH's. vlt runs it in batch mode: it never prompts and has no password support, and an unknown host key is refused unless the user's ssh config accepts new keys (`StrictHostKeyChecking accept-new` or `no`). Secrets and the wrapped vault key are age-encrypted before they are sent; `vlt.json` (the format number) and `vlt.lock` (host, user, PID, time) are plain text. The server learns the slugified group and secret names, file sizes and times. Whoever has the server's files can guess passphrases offline against `vlt.age`, as with any copy of the vault; whoever controls the server can also delete files or roll any file back to an older version, which vlt cannot detect (age has no rollback protection)
- Two copies of vlt using one vault (two computers sharing a server vault, or a synced folder) are safe for different secrets, and creating a secret never overwrites one; for the same secret the last save wins. `vlt.lock` warns about this but cannot prevent it
- A vault on a network share or cloud-synced folder (rather than over SFTP) depends on that file system's rename and link semantics; that is the user's responsibility
- All vault writes go through a temp-file + fsync + rename + directory-fsync helper, so a crash or full disk never leaves a truncated `.age` file (directory fsync is skipped on Windows). Atomicity is per file, so multi-file operations are ordered so any interrupted prefix leaves the vault readable: passphrase change rewrites only `vlt.age`, and format-1 migration writes the new key to a staging file before touching secrets and swaps `vlt.age` only after every secret is re-encrypted
- vlt creates vault directories with mode `0700` and files with `0600` (Unix). A directory that already existed keeps its mode, and vlt does not re-check or repair permissions later, so permissions widened by other tools stay widened

