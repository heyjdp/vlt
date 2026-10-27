# Changelog

All notable changes to vlt are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Releases will use
[Semantic Versioning](https://semver.org/), starting from the first tagged
release; until then, builds are identified by commit hash.

## [Unreleased]

### Added
- A create-vault screen, shown when no vault exists at the configured location, which names the target folder and asks for the passphrase twice (at least 12 characters).
- An auto-lock warning 30 seconds before locking, with *Stay unlocked* and *Lock now*, and an *Auto-lock* on/off setting.
- Settings (vault location, auto-lock timeout, clipboard TTL) are saved and survive a restart. Settings provided by environment variables are shown as such and never saved.
- Automatic, crash-safe migration of existing vaults to the new format on first unlock.
- The import completion message shows the file's SHA-256.
- The build version (`git describe`) in the window title and logs, embedded by `make build` and CI.
- An MIT `LICENSE` and this changelog.

### Changed
- **New vault format (format 2).** Each vault has a random X25519 vault key, stored in `vlt.age` encrypted to your passphrase, and secrets are encrypted to the vault key. Unlocking costs one scrypt derivation however many secrets you have, and changing the passphrase rewrites only `vlt.age`, atomically. `vlt.json` records the format.
- Vault work (unlock, save, delete, import, export, passphrase change) runs off the UI thread, with progress shown while scrypt runs.
- Auto-lock measures real inactivity (typing, selections, dialog answers) instead of resetting only on toolbar clicks.
- Locking dismisses open dialogs, empties loaded secrets and blanks forms, and clears the clipboard if it still holds vlt's copy.
- The README and design documents describe memory protection as best-effort and name its limits.
- The TOTP secret in record forms is masked like a password.

### Removed
These were part of the internal store, crypto and ui APIs (`internal/` packages), not a public interface:
- `Store.Save`, replaced by `Store.Create` (never overwrites) and `Store.Update`.
- `Store.ReEncryptAll`, `Store.IsNew`, `Store.Verify`, `Store.CreateSentinel` and `Store.RecreateSentinel`, superseded by `Create`, `Open`, `Exists` and `ChangePassphrase` in the vault-key design.
- `Store.SetDir`, which was unsynchronised (moving a vault now relocks instead).
- `crypto.NewIdentity` and `Identity.ScryptIdentity`, replaced by `GenerateIdentity`, `ParseIdentity`, `WrapIdentity` and `UnwrapIdentity`.
- `DetailView.SetOnSave` and `FileOps.SetOnSave`, replaced by callbacks wired once when the main view is built.
- Unused code: `Store.Groups` (group suggestions now come from the loaded list), `store.ValidSecretTypes`, `Identity.IsZeroed` and `App.ShowError`, and hand-written string helpers replaced by the standard library.

### Fixed
- Every vault write is atomic (temp file, fsync, rename), so a crash or full disk can no longer truncate a secret.
- Changing the passphrase can no longer strand secrets under the old passphrase.
- Creating a secret never overwrites another, including one whose name differs only in case or punctuation.
- A vault folder that holds secrets but lost its `vlt.age` is no longer adopted by any passphrase; a mistyped first passphrase can no longer become the vault's passphrase.
- Moving a vault into a folder that already holds one is refused instead of overwriting its key.
- Editing or deleting a secret selected from the list now updates the list.
- The clipboard is cleared only if it still holds the value vlt copied, each copy gets its full TTL, and the clipboard is cleared on lock and on quit.
- Importing a file fails cleanly on a read error instead of saving a truncated file.
- Passphrase fields are cleared as soon as they are read.

### Security
- 13 vulnerabilities reachable from vlt's code (9 in the Go standard library, 4 in `golang.org/x/image`) are fixed by the patched toolchain `go1.27.3` and `golang.org/x/image` v0.45.0. Unreachable advisories in `golang.org/x/net`, `golang.org/x/crypto`, `golang.org/x/text` and `goldmark` were also cleared by dependency updates.
- CI runs `govulncheck` on every push and pull request, and its GitHub Actions are pinned to commit SHAs with read-only permissions.
