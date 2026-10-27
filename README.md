# vlt

A cross-platform GUI secret manager using [age](https://age-encryption.org/) encryption.

vlt replaces ad-hoc PGP-encrypted text file workflows with a structured, portable vault. Secrets are stored as individual `.age` files in a directory, organised by group, unlocked by a single passphrase. The only key material on disk is the vault key, stored encrypted to your passphrase inside the vault itself. The vault directory is fully portable — copy it anywhere and unlock with the same passphrase.

## Features

- **Passphrase-only unlock** — no external key files, no OS keychain dependency
- **Safe vault creation** — when no vault exists at the configured location (first run, or after switching to an empty or missing folder), a dedicated screen shows where the vault will be created and asks for the passphrase twice (at least 12 characters not counting leading or trailing spaces; five or more random words recommended), so a typo can never become the passphrase. If the vault directory cannot be read (for example, permission denied), vlt shows the error and a Retry button rather than offering to create a vault over it
- **age encryption** — each secret is an individual `.age` file encrypted to a random X25519 vault key; the vault key is encrypted to your passphrase with scrypt, so unlocking costs one scrypt derivation however many secrets you have
- **Secure memory (best-effort)** — the vault key's encoded form is held in mlock'd memory via memguard and destroyed on lock; passphrase fields are cleared as soon as they are read, and on lock loaded secrets are emptied and vlt's references to them dropped. Go cannot wipe strings, so the parsed key and secret text live on the heap until garbage-collected
- **Secret types** — passwords, structured login records (URL/username/password/TOTP; the password and TOTP secret are masked), and encrypted files
- **Groups** — organise secrets into groups (work, personal, etc.) with a dropdown that shows existing groups and accepts new ones
- **Search** — instant filter across secret names, groups, and tags
- **Clipboard safety** — copied values are cleared after a configurable TTL (default 30s) with a countdown in the status bar. Each copy gets its own full TTL; the clipboard is cleared only if it still holds the value vlt copied (anything you copied since from another app is left alone); locking the vault or quitting vlt clears it immediately
- **Auto-lock** — vault locks after a configurable period of inactivity (default 5 minutes), zeroing the vault key from memory. Typing, pressing buttons, selecting secrets, moving sliders and answering dialogs count as activity; scrolling and pointer movement do not. With a timeout of a minute or more, a warning appears 30 seconds before locking: *Stay unlocked* (or any other input) restarts the countdown and *Lock now* locks immediately
- **File import/export** — import files via picker (base64-encoded, MIME detected, 10 MiB limit, SHA-256 shown on completion), export to disk via save dialog. Reading, encoding and writing run in the background. A read error fails the import and saves nothing; a failed export write or close is reported and the partial file is removed where possible. Dialogs left open (including file pickers) are dismissed when the vault locks, so an export can never be written after a lock
- **Passphrase verification** — a wrong passphrase fails immediately when `vlt.age` (the wrapped vault key) cannot be decrypted
- **Change passphrase** — atomically rewrites only `vlt.age`; secrets are never re-encrypted, so an interrupted change leaves the old passphrase working
- **Automatic migration** — vaults from earlier versions (every secret encrypted directly to the passphrase) are upgraded on first unlock; the upgrade is crash-safe and resumes if interrupted
- **Move/switch vault** — copy secrets (including the wrapped key) to a new location, or point to a different vault directory
- **Cross-platform** — Linux, macOS, Windows, OpenBSD

## Vault Structure

```
vault/
├── vlt.age                  # Vault key, encrypted to the passphrase (scrypt)
├── vlt.json                 # Format marker: {"format":2}
├── work/
│   ├── github-api-key.age
│   └── jira-login.age
├── personal/
│   ├── email.age
│   └── bank-login.age
└── default/
    └── misc-token.age
```

- Secrets are stored at `vault/<group>/<slug>.age`
- Groups are plain subdirectories, created on demand. The directory is the slugified group name; the secret keeps the group name as you typed it (`Personal Finance` is shown as typed and stored in `personal-finance/`). Group names that slugify to the same directory are one group, and a new or imported secret typed into `personal finance` joins an existing `Personal Finance` group. A group name with no ASCII letter or digit becomes `default`.
- Filenames are slugified from the secret name (lowercase ASCII letters, digits and `-`). Names that slugify to the same filename, such as `GitHub Key` and `github-key`, cannot coexist in a group: creating one when the other exists is refused, and updating through the other name is refused. Names with no ASCII letter or digit, or whose filename would exceed 255 bytes, are rejected.
- No database, no index file — the directory structure is the index
- Every write is atomic: vlt writes a `.tmp-*` file beside the target, fsyncs it, renames it into place, and fsyncs the directory (skipped on Windows, where NTFS journals the rename). New group directories are fsynced into their parent too. A crash leaves either the old or the new file, never a partial one. Leftover `.tmp-*` files are ignored and can be deleted.
- Trivially Git-compatible (add `.tmp-*` to the vault's `.gitignore` so an interrupted write never shows up as untracked)

### Default Vault Location

| OS      | Path                                       |
|---------|--------------------------------------------|
| macOS   | `~/Library/Application Support/vlt/vault/` |
| Linux   | `~/.local/share/vlt/vault/`                |
| Windows | `%APPDATA%\vlt\vault\`                     |
| OpenBSD | `~/.local/share/vlt/vault/`                |

Override with the `VLT_VAULT_DIR` environment variable, or move or switch the vault in Settings; the choice is saved (see [Configuration](#configuration)).

## Secret Envelope Format

Every `.age` file decrypts to a JSON envelope:

```json
{
  "version": 1,
  "type": "password",
  "name": "GitHub API Key",
  "group": "work",
  "created_at": "2026-03-14T10:00:00Z",
  "modified_at": "2026-03-14T10:00:00Z",
  "tags": ["github", "ci"],
  "notes": "Expires 2027-03-14",
  "value": "ghp_xxxxxxxxxxxx"
}
```

Three secret types share this structure:

- **password** — single-value secrets (API keys, tokens). Uses the `value` field.
- **record** — structured logins. Uses `username`, `password`, `url`, `totp_secret` fields.
- **file** — binary files (SSH keys, certificates). Uses `value` (base64), `original_filename`, `mime_type`, `encoding` fields. 10 MiB size limit.

## Requirements

- **Go 1.26.1+** — [install instructions](https://go.dev/doc/install), or `brew install go` on macOS. `go.mod` also names a patched toolchain (`toolchain go1.27.3`), which the `go` command downloads automatically if yours is older; it fixes standard-library vulnerabilities that `govulncheck` reports
- **A C compiler** — Fyne's desktop driver uses cgo (Xcode command-line tools on macOS, gcc on Linux, MinGW-w64 on Windows)
- **OpenGL/Metal** — required by Fyne for rendering (system libraries, no manual install needed on macOS/Windows; on Linux install `libgl1-mesa-dev xorg-dev libxkbcommon-dev`)
- **OpenBSD** — install `mesa-dri` via `pkg_add` for OpenGL support

### Optional Tools

| Tool | Purpose | Install |
|------|---------|---------|
| golangci-lint | Linting and formatting (gofumpt, goimports) | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0` |
| govulncheck | Vulnerability scanning | `go install golang.org/x/vuln/cmd/govulncheck@v1.9.0` |
| lefthook | Git hooks | `go install github.com/evilmartians/lefthook@latest` |

## Clone and Build

> The module path `github.com/yourorg/vlt` and the app ID `io.yourorg.vlt` are placeholders. Renaming the app ID moves Fyne's saved settings to a new location, so a rename should migrate them from the old ID.

```bash
git clone https://github.com/yourorg/vlt.git
cd vlt

# Install dependencies
go mod download

# Build (embeds the version from `git describe`, shown in the window title)
make build

# Or with go directly (the version shows as "dev")
go build -trimpath -ldflags="-s -w" -o vlt ./cmd/vlt

# Run
./vlt
```

Or run directly without building a binary:

```bash
go run ./cmd/vlt
```

### Building for Other Platforms

Fyne's desktop driver uses cgo (OpenGL and GLFW), so a plain `GOOS=… go build` cannot cross-compile vlt: it needs a C compiler and system headers for the target, and `CGO_ENABLED=0` does not build at all. Build on each target OS instead (this is what CI does on Linux, macOS and Windows runners):

```bash
go build -trimpath -ldflags="-s -w" -o vlt ./cmd/vlt
```

For release builds of several platforms from one machine, use [fyne-cross](https://github.com/fyne-io/fyne-cross), which runs the build in Docker containers with the right toolchains. OpenBSD builds must be done on OpenBSD.

### Fyne Native Packages

Produces `.app` (macOS), `.exe` with icon (Windows), `.tar.gz` (Linux). `fyne package` packages for the OS it runs on; packaging for another OS needs `fyne-cross`:

```bash
go install fyne.io/fyne/v2/cmd/fyne@latest

fyne package -os darwin -icon Icon.png -name "vlt" -appID io.yourorg.vlt
fyne package -os windows -icon Icon.png -name "vlt" -appID io.yourorg.vlt
fyne package -os linux -icon Icon.png -name "vlt" -appID io.yourorg.vlt
```

## Testing

```bash
# Run all tests
go test ./...

# Run tests with race detector (recommended, used in CI)
go test -race ./...

# Run a specific package
go test -race ./internal/crypto/...
go test -race ./internal/store/...
go test -race ./internal/ui/...

# Verbose output
go test -race -v ./...

# With coverage report
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

The test suite covers:

- **crypto** — vault-key generation, wrap/unwrap with a passphrase, wrong-passphrase rejection, zeroing, encrypt/decrypt round-trips, X25519-only secret encryption, legacy passphrase decryption
- **store** — secret types and JSON marshalling, slugification, name validation, group directories and labels (kept as typed), CRUD operations (create/update/get/delete/list), refusal to overwrite on create and on slug collision, vault create/open/exists, one-scrypt-per-unlock check, passphrase change (only `vlt.age` rewritten, failures keep the old passphrase), format-1 migration (including resume after a crash), atomic writes, vault copy, `List` stopping when its context is cancelled
- **ui** — headless [`fyne/test`](https://pkg.go.dev/fyne.io/fyne/v2/test), with inline or queued runners for background work, hand-fired fake timers, and real vaults created with a cheap scrypt work factor:
  - *Screens*: create (passphrase confirmation and validation), unlock (wrong passphrase), and the error screen for an unreadable vault directory
  - *Background work*: unlock and create run off the UI thread with progress; stale results after a vault switch or lock are dropped; lock cancels the initial list load
  - *List and detail*: saves, deletes and imports update rows in place (also under a search filter and during the first load); forms save every field, mask secrets, and copy the right value; group suggestions show one spelling per group, a new or imported secret typed into a differently spelled group uses the existing one, and search matches a group by either spelling
  - *Auto-lock*: activity resets the countdown, idleness locks, the warning and its buttons work, timeout changes apply, zero disables it
  - *Clipboard*: cleared after the TTL, each copy gets its full TTL, unrelated content is left alone, cleared on lock and quit
  - *Import and export*: read errors and oversized files save nothing; the SHA-256 is reported; failed writes and closes are reported
  - *Settings*: env → saved → default precedence, clamping, per-setting saves, env-controlled settings disabled
  - *Memory hygiene*: passphrase entries cleared on read; lock empties secrets, blanks forms and wipes a pending export
  - *End-to-end flows through the real dialogs*: delete confirmation (Yes and No), import with group selection and replace (Yes and No), passphrase change then re-unlock, switch vault, export to a file

Crypto and store tests run in parallel (`t.Parallel()`). Most ui tests run serially, because `fyne/test` replaces the process-global current app. Every test uses `t.TempDir()` for filesystem isolation, and CI runs the whole suite under the race detector.

## Linting and Static Analysis

```bash
# Format (gofumpt + goimports, as configured in .golangci.yml)
golangci-lint fmt ./...

# Vet
go vet ./...

# Lint, including a formatting check (requires golangci-lint v2)
golangci-lint run ./...

# Vulnerability scan (requires govulncheck)
govulncheck ./...

# Run everything
make check
```

Use golangci-lint v2.14 or later: earlier releases cannot read the export data of the Go 1.27.3 standard library this module's toolchain uses, and fail with "export data version" errors.

The linter set (`.golangci.yml`) is golangci-lint's `standard` set plus `bodyclose`, `dupl`, `errorlint`, `gocritic`, `gosec`, `misspell`, `nilerr`, `revive`, `unconvert` and `unparam`. Two gosec rule groups are excluded for `internal/store`, with the reasons recorded in the config: file paths there are slugified names inside the user's vault directory, and the secret envelope's `password` field exists to be encrypted.

### Continuous integration

`.github/workflows/ci.yml` runs on pushes and pull requests to `main`:
- **lint** — golangci-lint (pinned), which also checks formatting
- **test** — `go vet` and `go test -race` on Linux (the UI tests are headless)
- **vuln** — `govulncheck` (pinned)
- **build** — native builds on Linux, macOS and Windows with cgo enabled, since Fyne cannot be cross-compiled with `CGO_ENABLED=0`

Every action is pinned to a commit SHA, checkout does not persist credentials, and the workflow only has read access to the repository.

## Configuration

| Environment Variable     | Default   | Range                     | Description                          |
|--------------------------|-----------|---------------------------|--------------------------------------|
| `VLT_VAULT_DIR`         | (OS path) |                           | Override the vault directory location |
| `VLT_LOCK_TIMEOUT_MS`   | `300000`  | 30000–1800000, or 0 (off) | Auto-lock after N ms of inactivity   |
| `VLT_CLIPBOARD_TTL_S`   | `30`      | 5–120                     | Clear clipboard after N seconds      |

All settings can also be changed from the Settings screen, including turning auto-lock off. Changes take effect immediately and are saved in Fyne's per-app preferences, so the auto-lock timeout and clipboard TTL survive a restart. A new vault location (Move or Switch) is saved once a vault has been opened or created there, so a mistyped path never sticks.

Each setting is resolved in this order: environment variable, then the saved value, then the default. A setting provided by an environment variable is shown as such and cannot be changed in Settings, and it is never written to the saved preferences. Out-of-range values are clamped and unparsable ones ignored, with a warning in the log.

## Architecture

```
cmd/vlt/main.go              Entry point, config, Fyne bootstrap
internal/
├── crypto/
│   ├── identity.go          Vault key (X25519) in memguard; wrap/unwrap with passphrase (scrypt)
│   └── vault.go             Encrypt/decrypt secrets to the vault key
├── store/
│   ├── secret.go            Secret types, JSON envelope, slugification, group directories and labels
│   ├── store.go             Secret CRUD, list, copy
│   ├── vault.go             Create/Open/ChangePassphrase, format marker
│   ├── migrate.go           Crash-safe format-1 → format-2 migration
│   └── atomic.go            Atomic file writes
├── ui/
│   ├── app.go               Lock/unlock state machine, toolbar, layout
│   ├── unlock.go            Passphrase entry screen (existing vault)
│   ├── create.go            Create-vault screen (passphrase + confirmation)
│   ├── runner.go            Runs vault I/O off the UI thread, results via fyne.Do
│   ├── activity.go          Tracks user input for the auto-lock countdown
│   ├── clipboard.go         Copies secrets and clears them after the TTL or on lock
│   ├── config.go            Settings: load (env → saved → default), clamp, save
│   ├── list.go              Secret browser with search/filter
│   ├── detail.go            View/edit forms for all secret types, clipboard copy
│   ├── fileops.go           File import (picker, base64, MIME) and export
│   ├── settings.go          Change passphrase, vault location, lock timeout, clipboard TTL
│   └── theme.go             Custom Fyne theme (reduced font sizes)
└── testutil/
    └── fixtures.go          Shared test helpers and secret builders
```

### Layered Design

```
┌─────────────────────────────────┐
│         Interface Layer         │  cmd/vlt, internal/ui — Fyne GUI
├─────────────────────────────────┤
│         Application Layer       │  internal/store — orchestration, CRUD
├─────────────────────────────────┤
│          Domain Layer           │  internal/store — types, validation
├─────────────────────────────────┤
│       Infrastructure Layer      │  internal/crypto — age, memguard
└─────────────────────────────────┘
```

- The domain and crypto layers have zero knowledge of the GUI
- The UI layer is thin — it delegates to the store and crypto packages
- `internal/` enforces package boundaries at the compiler level

## Design Decisions

### One scrypt-wrapped vault key

Each vault has a random age X25519 identity, the **vault key**. Every secret is encrypted to it, which costs microseconds. The vault key itself is stored in `vlt.age`, encrypted to the passphrase with age's scrypt recipient (work factor N=2^18, random salt). Unlocking therefore runs exactly one scrypt derivation regardless of how many secrets the vault holds, and a wrong passphrase fails immediately on that one file.

An offline attacker with the vault directory still pays one scrypt per passphrase guess, as before, so security depends on passphrase strength. Changing the passphrase rewraps only `vlt.age`, atomically.

The vault stays fully portable: copy the directory anywhere and unlock with the same passphrase. There is no OS keychain dependency, which matters for OpenBSD support. Because `vlt.age` holds the only copy of the vault key, back it up with the rest of the vault.

### Format versions and migration

`vlt.json` records the on-disk format. Format 1 (earlier versions) encrypted every secret directly to the passphrase and used `vlt.age` as a sentinel containing the word "unlocked". On first unlock of a format-1 vault, vlt migrates it in place:

1. Generate a vault key and write it, wrapped, to `vlt.migrating.age`.
2. Re-encrypt each secret to the vault key (one atomic write per file; files already migrated are skipped).
3. Atomically replace `vlt.age` with the wrapped key.
4. Write `vlt.json` and delete `vlt.migrating.age`.

A crash at any step leaves a vault that the next unlock finishes migrating. Secrets that do not decrypt with the passphrase are left untouched and reported in a dialog.

### Group-based directory structure

Secrets are stored at `vault/<group>/<slug>.age` rather than by type. Groups are user-defined (e.g. "work", "personal") and created as subdirectories on demand. This keeps the vault browsable in a file manager and makes it natural to organise secrets the way users think about them. The type (password/record/file) is stored in the JSON envelope, not in the path.

### JSON envelope inside age

Each `.age` file decrypts to a JSON document rather than raw secret data. This allows storing metadata (name, type, group, tags, timestamps, notes) alongside the secret value in a single encrypted file, with no unencrypted index or manifest.

### Fyne for the GUI

Fyne produces a single binary with no runtime dependencies beyond the system's OpenGL libraries (it links them dynamically through cgo). It supports Linux, macOS, Windows, and OpenBSD. The alternative (Qt via PySide6) has incomplete OpenBSD support and requires a bundled runtime for distribution.

### memguard for secure memory

The vault key's encoded form is stored in a memguard LockedBuffer, which provides mlock (prevents swapping to disk), guard pages (detect buffer overflows), and explicit zeroing. The buffer is destroyed on lock and on exit.

This is best-effort, and the limits are worth stating plainly. Go strings cannot be wiped, so age's parsed `X25519Identity`, the passphrase as typed, and every decrypted secret shown in the UI live on the ordinary Go heap until the garbage collector reclaims them. vlt narrows the exposure instead:
- passphrase fields are cleared as soon as they are read
- on lock, loaded secrets are emptied and the form's fields blanked (Fyne may keep old widgets cached for a while), vlt's references to them are dropped, open dialogs are dismissed, and the clipboard is cleared if it still holds vlt's copy
- decrypted byte buffers that vlt owns are zeroed after use (copies made inside libraries are not)

It does not, and in Go cannot, guarantee that secret text is erased from memory.

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## License

MIT — see [LICENSE](LICENSE).
