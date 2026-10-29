# Changelog

All notable changes to vlt are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Releases use [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed
- The module path is `github.com/heyjdp/vlt` and the app ID `io.github.heyjdp.vlt` (they were the placeholders `github.com/yourorg/vlt` and `io.yourorg.vlt`). Fyne keeps settings per app ID, so on its first start vlt carries the vault location, auto-lock timeout and clipboard TTL over from 0.1.0's settings file. On macOS the app's bundle ID changes too, so Gatekeeper asks once more (*Open Anyway*).

## [0.1.0] - 2026-10-29

### Added
- **An About dialog**, from the *About* button in the status bar (on every screen, locked or not) and the *About* menu item (*vlt → About vlt* on macOS, *Help → About* elsewhere). It shows the version, commit, platform and Go and Fyne versions, with *Copy Details* and *Report a Bug…* (a new GitHub issue with the details filled in). The details hold no secrets, paths or names.
- **Vaults on an SSH server.** A vault location can be a folder on a server, as `[user@]host:path` or `ssh://[user@]host[:port]/path`, in Settings (Move, Switch), on the create screen (*Use a Vault on a Server…*) and in `VLT_VAULT_DIR`. vlt speaks SFTP over the system's `ssh` client, so `~/.ssh/config`, keys, `ssh-agent` and `known_hosts` apply. Writes stay atomic, and creating a secret still never overwrites one. Secrets and the vault key are encrypted before they are sent; `vlt.json` and `vlt.lock` are plain text. If the server can't be reached when vlt checks the location, it shows the error with *Retry* and (unless `VLT_VAULT_DIR` sets the location) *Use the Vault on This Computer*.
- **A lock file, `vlt.lock`,** records who has a vault open. Unlocking a vault that another copy of vlt has open shows who and since when, and asks before going on. A lock left by a crashed copy on the same computer is taken over automatically. Quitting releases the lock, and an unanswered question closes the vault after the auto-lock timeout.
- The unlock screen names the vault it unlocks, and the status bar names the server of a remote vault.
- A create-vault screen, shown when no vault exists at the configured location, which names the target folder and asks for the passphrase twice (at least 12 characters).
- *Open Existing Vault…* on the create-vault screen, to reopen a vault kept in another folder (for example on a new Mac or after preferences were cleared). The location is saved once the vault unlocks.
- An auto-lock warning 30 seconds before locking, with *Stay unlocked* and *Lock now*, and an *Auto-lock* on/off setting.
- Settings (vault location, auto-lock timeout, clipboard TTL) are saved and survive a restart. Settings provided by environment variables are shown as such and never saved. The vault location is saved whenever a vault is unlocked or created.
- Automatic, crash-safe migration of existing vaults to the new format on first unlock.
- The import completion message shows the file's SHA-256.
- The build version (`git describe`) in the window title and logs, embedded by `make build` and CI.
- An MIT `LICENSE` and this changelog.
- Screenshots in the README, and `make screenshots` to re-render them headlessly.
- Installable packages: a macOS app (`vlt.app`, Apple silicon, ad-hoc signed) and a Linux AppImage (x86-64), built by CI after the checks pass and published as a GitHub release for `v*` tags. An app icon, and `make package-mac` and `make package-linux` to build them locally.
- A *Your Data* section in the README: where the vault and settings live, what survives uninstalling and reinstalling, how to back up, restore or move a vault, and what happens if you forget or change the passphrase.

### Changed
- **A redesigned interface.** A teal accent taken from the app icon, with light and dark palettes in which body text, secondary text and text on accent buttons meet WCAG AA contrast. Unlock, create and error screens are centred cards; unlock and create show the app icon. The main view has a header bar with icon buttons and a sidebar whose rows show each secret's type icon, name and group, with a count of the secrets shown. Secrets open with a header (icon, name, type and group) above a labelled form in a card, an action row with Delete on the right, and readable timestamps. Settings are grouped in cards. Long forms now scroll instead of being cut off, and wrapped text on the create screen no longer clips. The window has the app icon.
- **New vault format (format 2).** Each vault has a random X25519 vault key, stored in `vlt.age` encrypted to your passphrase, and secrets are encrypted to the vault key. Unlocking costs one scrypt derivation however many secrets you have, and changing the passphrase rewrites only `vlt.age`, atomically. `vlt.json` records the format.
- Vault work (unlock, save, delete, import, export, passphrase change) runs off the UI thread, with progress shown while scrypt runs.
- Auto-lock measures real inactivity (typing, selections, dialog answers) instead of resetting only on toolbar clicks.
- Locking dismisses open dialogs, empties loaded secrets and blanks forms, and clears the clipboard if it still holds vlt's copy.
- The README and design documents describe memory protection as best-effort and name its limits.
- The TOTP secret in record forms is masked like a password.
- On Linux and the BSDs the default vault follows `XDG_DATA_HOME` (`$XDG_DATA_HOME/vlt/vault`, or `~/.local/share/vlt/vault` when it is unset or relative). A vault already in `~/.local/share/vlt/vault` is still used when the XDG location holds none.

### Removed
These were part of the internal store, crypto and ui APIs (`internal/` packages), not a public interface:
- `Store.Save`, replaced by `Store.Create` (never overwrites) and `Store.Update`.
- `Store.ReEncryptAll`, `Store.IsNew`, `Store.Verify`, `Store.CreateSentinel` and `Store.RecreateSentinel`, superseded by `Create`, `Open`, `Exists` and `ChangePassphrase` in the vault-key design.
- `Store.SetDir`, which was unsynchronised (moving a vault now relocks instead).
- `crypto.NewIdentity` and `Identity.ScryptIdentity`, replaced by `GenerateIdentity`, `ParseIdentity`, `WrapIdentity` and `UnwrapIdentity`.
- `DetailView.SetOnSave` and `FileOps.SetOnSave`, replaced by callbacks wired once when the main view is built.
- Unused code: `Store.Groups` (group suggestions now come from the loaded list), `store.ValidSecretTypes`, `Identity.IsZeroed`, `App.ShowError` and the test-only `UnlockScreen.PassphraseEntry`, and hand-written string helpers replaced by the standard library.

### Fixed
- Opening or switching to the folder above a vault (for example `…/vlt` instead of `…/vlt/vault`) no longer treats the vault as a group of a legacy vault and "migrates" it, which re-encrypted the inner vault's key so its own passphrase stopped working. A subfolder that is itself a vault is now ignored when looking for secrets, and *Open Existing Vault…* points to it instead.
- A vault whose `vlt.age` is missing says so in plain words and suggests restoring it from a backup, even if `vlt.json` is gone too (previously reported as an incorrect passphrase).
- Group names are kept as typed (`Personal Finance`) instead of being replaced by their directory name (`personal-finance`). A new or imported secret whose group differs from an existing group only in case or punctuation uses the existing spelling. Groups saved by earlier versions keep their slugified name, and new secrets in them use that spelling too.
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
