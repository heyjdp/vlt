# vlt

A cross-platform GUI secret manager using [age](https://age-encryption.org/) encryption.

vlt replaces ad-hoc PGP-encrypted text file workflows with a structured, portable vault. Secrets are stored as individual `.age` files in a directory, organised by group, unlocked by a single passphrase. No key files are written to disk. The vault directory is fully portable — copy it anywhere and unlock with the same passphrase.

## Features

- **Passphrase-only unlock** — no key files, no OS keychain dependency
- **age encryption** — each secret is an individual `.age` file encrypted with scrypt
- **Secure memory** — derived identity held in mlock'd memory via memguard, zeroed on lock
- **Secret types** — passwords, structured login records (URL/username/password/TOTP), and encrypted files
- **Groups** — organise secrets into groups (work, personal, etc.) with a dropdown that shows existing groups and accepts new ones
- **Search** — instant filter across secret names, groups, and tags
- **Clipboard safety** — copied values are cleared after a configurable TTL (default 30s) with a countdown in the status bar
- **Auto-lock** — vault locks after configurable inactivity timeout (default 5 minutes), zeroing the identity from memory
- **File import/export** — import files via picker (base64-encoded, MIME detected, 10 MB limit), export to disk via save dialog
- **Passphrase verification** — a sentinel file (`vlt.age`) verifies the passphrase on unlock, giving an immediate error for wrong passphrases instead of failing on individual secret decryption
- **Change passphrase** — re-encrypts every secret and the sentinel file with the new passphrase
- **Move/switch vault** — copy secrets (including sentinel) to a new location, or point to a different vault directory
- **Cross-platform** — Linux, macOS, Windows, OpenBSD

## Vault Structure

```
vault/
├── vlt.age                  # Sentinel file (passphrase verification)
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
- Groups are plain subdirectories, created on demand
- Filenames are slugified from the secret name
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

Override with the `VLT_VAULT_DIR` environment variable.

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
- **file** — binary files (SSH keys, certificates). Uses `value` (base64), `original_filename`, `mime_type`, `encoding` fields. 10 MB size limit.

## Requirements

- **Go 1.23+** — [install instructions](https://go.dev/doc/install), or `brew install go` on macOS
- **OpenGL/Metal** — required by Fyne for rendering (system libraries, no manual install needed on macOS/Windows; on Linux install `libgl1-mesa-dev xorg-dev`)
- **OpenBSD** — install `mesa-dri` via `pkg_add` for OpenGL support

### Optional Tools

| Tool | Purpose | Install |
|------|---------|---------|
| gofumpt | Opinionated formatting | `go install mvdan.cc/gofumpt@latest` |
| golangci-lint | Linting | `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` |
| govulncheck | Vulnerability scanning | `go install golang.org/x/vuln/cmd/govulncheck@latest` |
| lefthook | Git hooks | `go install github.com/evilmartians/lefthook@latest` |

## Clone and Build

```bash
git clone https://github.com/yourorg/vlt.git
cd vlt

# Install dependencies
go mod download

# Build
go build -trimpath -ldflags="-s -w" -o vlt ./cmd/vlt

# Run
./vlt
```

Or run directly without building a binary:

```bash
go run ./cmd/vlt
```

### Build with Version Tag

```bash
go build -trimpath -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty)" -o vlt ./cmd/vlt
```

### Cross-Compilation

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o vlt-linux-amd64 ./cmd/vlt

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o vlt-darwin-arm64 ./cmd/vlt

# Windows
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o vlt-windows-amd64.exe ./cmd/vlt

# OpenBSD
GOOS=openbsd GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o vlt-openbsd-amd64 ./cmd/vlt
```

### Fyne Native Packages

Produces `.app` (macOS), `.exe` with icon (Windows), `.tar.gz` (Linux):

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

# Verbose output
go test -race -v ./...

# With coverage report
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

The test suite has 43 tests covering:

- **crypto** — identity creation, zeroing, encrypt/decrypt round-trips, wrong-passphrase rejection, zeroed-identity errors, invalid ciphertext
- **store** — secret types and JSON marshalling, slugification, name validation, group normalisation, CRUD operations (save/get/delete/list), group listing, re-encryption, vault copy, sentinel create/verify/wrong-passphrase/recreate, vault structure verification

All tests use `t.Parallel()`, the race detector, and `t.TempDir()` for filesystem isolation.

## Linting and Static Analysis

```bash
# Format check
gofumpt -l .

# Vet
go vet ./...

# Lint (requires golangci-lint)
golangci-lint run ./...

# Vulnerability scan (requires govulncheck)
govulncheck ./...

# Run everything
make check
```

## Configuration

| Environment Variable     | Default   | Description                          |
|--------------------------|-----------|--------------------------------------|
| `VLT_VAULT_DIR`         | (OS path) | Override the vault directory location |
| `VLT_LOCK_TIMEOUT_MS`   | `300000`  | Auto-lock after N ms of inactivity   |
| `VLT_CLIPBOARD_TTL_S`   | `30`      | Clear clipboard after N seconds      |

All settings can also be changed at runtime from the Settings screen.

## Architecture

```
cmd/vlt/main.go              Entry point, config, Fyne bootstrap
internal/
├── crypto/
│   ├── identity.go          Passphrase → age ScryptIdentity, memguard secure storage
│   └── vault.go             Encrypt/decrypt wrappers around filippo.io/age
├── store/
│   ├── secret.go            Secret types, JSON envelope, slugification, group normalisation
│   └── store.go             Vault directory CRUD, sentinel file, re-encryption, copy
├── ui/
│   ├── app.go               Lock/unlock state machine, toolbar, layout
│   ├── unlock.go            Passphrase entry screen
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

### Passphrase-only, no key files

The vault uses age's scrypt recipient exclusively. No key file is ever written to disk. The derived identity exists only in process memory (in a memguard LockedBuffer with mlock and guard pages) and is zeroed on lock or exit. This makes the vault fully portable — copy the directory anywhere and unlock with the same passphrase. It also avoids OS-specific keychain dependencies, which is important for OpenBSD support.

age's scrypt implementation generates a random salt per file and uses a work factor of N=2^18, making brute-force expensive. Security depends on passphrase strength.

### Sentinel file for passphrase verification

On first run, `vlt.age` is created in the vault root containing the word "unlocked" encrypted with the chosen passphrase. On subsequent unlocks, this file is decrypted to verify the passphrase immediately, rather than discovering a wrong passphrase only when trying to decrypt individual secrets. The sentinel is re-encrypted when the passphrase is changed and copied when the vault is moved.

### Group-based directory structure

Secrets are stored at `vault/<group>/<slug>.age` rather than by type. Groups are user-defined (e.g. "work", "personal") and created as subdirectories on demand. This keeps the vault browsable in a file manager and makes it natural to organise secrets the way users think about them. The type (password/record/file) is stored in the JSON envelope, not in the path.

### JSON envelope inside age

Each `.age` file decrypts to a JSON document rather than raw secret data. This allows storing metadata (name, type, group, tags, timestamps, notes) alongside the secret value in a single encrypted file, with no unencrypted index or manifest.

### Fyne for the GUI

Fyne produces a single static binary with no runtime dependencies beyond OpenGL. It supports Linux, macOS, Windows, and OpenBSD. The alternative (Qt via PySide6) has incomplete OpenBSD support and requires a bundled runtime for distribution.

### memguard for secure memory

The derived age identity is stored in a memguard LockedBuffer, which provides mlock (prevents swapping to disk), guard pages (detect buffer overflows), and explicit zeroing. The buffer is destroyed on lock, on exit, and on passphrase change.

## License

See [LICENSE](LICENSE) for details.
