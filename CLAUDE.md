# CLAUDE.md — Expert Go Development
## Cross-Platform GUI Applications with Fyne

## Philosophy

Write Go that is **correct first, clear second, fast third**. Prefer the standard library over dependencies. Prefer composition over embedding over inheritance. Prefer explicit over implicit. Every package should have a single, well-defined responsibility. Errors are values — handle them at every call site. Code that is never executed is a liability — delete it, don't comment it out.

Go is not Python with braces. Lean into its idioms: small interfaces, explicit error returns, goroutines for concurrency, `context.Context` for cancellation. Never fight the language.

---

## When to Use This Skill

- Creating a new Go module or binary
- Setting up `go.mod` and workspace configuration
- Configuring development tools (linting, formatting, testing, building)
- Writing Go packages with external dependencies
- Designing cross-platform GUI applications with Fyne
- Planning package architecture and interface design
- Setting up CI/CD pipelines for multi-platform Go builds
- Debugging, profiling, and tracing Go applications
- Reviewing code for correctness and security
- Packaging and distributing Fyne GUI applications

---

## Anti-Patterns to Avoid

| Avoid | Use Instead |
|-------|-------------|
| `go get -u ./...` blindly | `go get <pkg>@<version>` then `go mod tidy` |
| Editing `go.mod` manually to add deps | `go get <pkg>` — let the toolchain manage it |
| `interface{}` everywhere | `any` (Go 1.18+) where truly needed; prefer concrete types |
| Ignoring returned errors | Handle every error; wrap with `fmt.Errorf("context: %w", err)` |
| `panic` for recoverable errors | Return `error`; reserve `panic` for truly unrecoverable state |
| `log.Fatal` / `log.Panic` in libraries | Return errors; only call `os.Exit` in `main` |
| `init()` for stateful side effects | Explicit `New*()` constructors with returned errors |
| Naked `return` in long functions | Named returns only where they genuinely clarify |
| `time.Now()` | `time.Now().UTC()` — always UTC |
| `fmt.Println` / `fmt.Printf` for logging | `log/slog` with structured key-value pairs |
| Global mutable state | Dependency injection via constructor parameters |
| Goroutine without a done signal | Always pair goroutines with `context.Context` cancellation |
| `sync.WaitGroup` across package boundaries | Return a `<-chan struct{}` or use `errgroup` |
| Unbuffered channels for event queues | Sized channels with documented capacity rationale |
| Copying a `sync.Mutex` | Embed `sync.Mutex` by pointer or use `noCopy` guard |
| `os.Open` without `defer f.Close()` | Always `defer` resource cleanup immediately after open |
| Stringly-typed identifiers | Define named types (`type SecretType string; const TypePassword SecretType = "password"`) |
| Flat package structure | Domain-driven `internal/` sub-packages with clear boundaries |
| `_test.go` files importing internal packages via reflection | Use exported test helpers or `internal/testutil` |
| `fyne.CurrentApp()` for global state | Pass `fyne.App` and `fyne.Window` explicitly |
| Fyne widget mutation off the main goroutine | Always call `widget.Refresh()` from the main goroutine or via `fyne.Do()` |

**Key principles:**
- Always use `go get` and `go mod tidy` to manage dependencies — never edit `go.mod` by hand
- All public types, functions, and constants must have doc comments (`// FuncName does X`)
- Use `context.Context` as the first argument to any function that can block or be cancelled
- Wrap errors at package boundaries with `fmt.Errorf("packagename.FuncName: %w", err)`

---

## Decision Tree

```
What are you doing?
│
├─ Single-file utility script?
│   └─ Single main.go with go:generate comments for any codegen
│
├─ New multi-package binary (not distributed)?
│   └─ Minimal module setup (see Quick Start below)
│
├─ New reusable library?
│   └─ Full module setup with exported API surface (see Full Setup below)
│
├─ Cross-platform GUI application?
│   └─ Fyne application layout (see Fyne Application Setup below)
│
└─ Existing project, adding a feature?
    └─ See Development Lifecycle below
```

---

## Tool Overview

| Tool | Purpose | Replaces |
|------|---------|----------|
| **go** | Build, test, dependency management | — (stdlib toolchain) |
| **golangci-lint** | Linting (aggregates 100+ linters) | flake8, pylint, individual linters |
| **gofumpt** | Opinionated formatting (superset of gofmt) | black, ruff format |
| **goimports** | Import organisation + gofmt | isort + gofmt |
| **staticcheck** | Deep static analysis | pyright, mypy |
| **go test** | Testing with -race and -cover | pytest |
| **govulncheck** | Dependency vulnerability scanning | pip-audit |
| **lefthook** | Pre-commit hooks (fast, no runtime) | prek, pre-commit |
| **fyne** CLI | Package, cross-compile, and bundle Fyne apps | PyInstaller, briefcase |

### Security Tools

| Tool | Purpose | When It Runs |
|------|---------|--------------|
| **shellcheck** | Shell script linting | pre-commit |
| **detect-secrets** | Secret detection in source | pre-commit |
| **actionlint** | GitHub Actions workflow validation | pre-commit, CI |
| **zizmor** | Workflow security audit | pre-commit, CI |
| **govulncheck** | Go dependency CVE scanning | CI, manual |
| **Dependabot** | Automated dependency updates | scheduled |
| **gosec** | Go-specific security linting | golangci-lint, CI |

### Debugging & Profiling Tools

| Tool | Purpose | When to Use |
|------|---------|-------------|
| **dlv** (Delve) | Interactive debugger | Investigating control flow, inspecting goroutines |
| **pprof** | CPU, memory, goroutine profiler | Performance bottlenecks, memory leaks |
| **go test -race** | Data race detector | All tests in CI; mandatory |
| **go test -trace** | Execution tracer | Goroutine scheduling and latency analysis |
| **benchstat** | Statistical benchmark comparison | Verifying performance changes |
| **fyne/test** | Fyne widget unit testing (no display) | GUI component correctness |

---

# Stage 1: Planning

Before writing code, define the problem boundary and make architectural decisions explicit.

## 1.1 Requirements Gathering

When the user describes what they want, extract and confirm:

- **Inputs**: What data enters the system? Formats, sources, volumes.
- **Outputs**: What must be produced? Formats, destinations, latency requirements.
- **Constraints**: OS targets, Go version minimum, CGO requirement (avoid unless necessary for Fyne), memory limits, licensing.
- **Interfaces**: GUI? CLI? Library? Both?
- **Failure modes**: What happens when things go wrong? Surface to user? Log and continue? Hard exit?

Produce a short written summary before proceeding to code. This catches misunderstandings early.

## 1.2 Architecture & Package Design

### Think in Layers

```
┌─────────────────────────────────┐
│         Interface Layer         │  cmd/, ui/ — Fyne widgets, CLI
├─────────────────────────────────┤
│         Application Layer       │  Use cases, orchestration, sessions
├─────────────────────────────────┤
│          Domain Layer           │  Core logic, types, rules
├─────────────────────────────────┤
│       Infrastructure Layer      │  Filesystem, crypto, OS services
└─────────────────────────────────┘
```

- Domain layer has **zero** external dependencies.
- Infrastructure adapters implement interfaces defined by the domain.
- Application layer wires domain and infrastructure together.
- Interface layer is thin — parse input, delegate, render output.
- `internal/` packages enforce package boundaries at the compiler level.

### Design Decisions to Make Early

| Decision | Options | Default |
|----------|---------|---------|
| Concurrency model | Goroutines + channels, `sync` primitives, `errgroup` | Goroutines with `context.Context` |
| Configuration | env vars, TOML/YAML file, CLI flags | `flag` stdlib or `cobra` + config file |
| Logging | `log/slog` (stdlib, Go 1.21+) | `slog` with structured JSON handler |
| Error strategy | Wrapped `error` values, sentinel errors, custom types | Wrapped errors at boundaries; sentinel errors for comparison |
| Serialisation | `encoding/json` stdlib | stdlib for JSON; avoid heavy frameworks |
| GUI state | Fyne reactive bindings vs. manual `Refresh()` | Fyne `binding` package for simple state; explicit refresh for complex |

### Interface Design Principles

- Define interfaces **at the point of use**, not at the point of implementation.
- Keep interfaces **small** — prefer single-method interfaces (`io.Reader`, `io.Writer`).
- Accept interfaces, return concrete types.
- Make invalid states unrepresentable: use named types, const enums, constructor validation.
- Public API surface should be intentionally small. Unexported symbols are the default.

```go
// Good — small, purposeful interface defined where it is consumed
type Decryptor interface {
    Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
}

// Good — accept interface, return concrete
func NewStore(d Decryptor, dir string) (*Store, error) { ... }

// Bad — large interface nobody can implement fully
type VaultManager interface {
    List() []Secret
    Get(name string) (*Secret, error)
    Save(s *Secret) error
    Delete(name string) error
    Export(name, path string) error
    Import(path string) (*Secret, error)
    Lock()
    Unlock(passphrase string) error
}
```

## 1.3 Dependency Evaluation

Before adding a dependency, ask:

1. **Is it necessary?** Can the standard library do this in ≤50 lines?
2. **Is it maintained?** Check: last release date, open issue count, bus factor, Go module proxy availability.
3. **Is it safe?** Run `govulncheck` against it. Check for known CVEs.
4. **Is it compatible?** Verify license compatibility. Prefer MIT, BSD, Apache 2.0.
5. **Is it stable?** Prefer modules at v1 or above with a changelog and semver tags.
6. **Does it require CGO?** Avoid CGO unless absolutely necessary — it breaks cross-compilation.

**Approved core dependencies for this project:**

| Package | Purpose | Notes |
|---------|---------|-------|
| `filippo.io/age` | Age encryption/decryption | Native Go, no CGO |
| `fyne.io/fyne/v2` | Cross-platform GUI | Requires OpenGL/Metal |
| `github.com/awnumar/memguard` | Secure in-memory buffers | mlock'd, guard pages |

---

# Stage 2: Project Setup

## Quick Start: Minimal Module

```bash
# Create module
mkdir vlt && cd vlt
go mod init github.com/yourorg/vlt

# Add core dependencies
go get filippo.io/age
go get fyne.io/fyne/v2
go get github.com/awnumar/memguard

# Tidy lockfile
go mod tidy

# Run
go run ./cmd/vlt
```

## Full Project Setup

### 2.1 Project Structure

```
vlt/
├── go.mod                       # Module definition — committed
├── go.sum                       # Dependency checksums — committed
├── Makefile
├── README.md
├── CHANGELOG.md
├── LICENSE
├── DESIGN.md
├── .github/
│   └── workflows/
│       └── ci.yml
├── .golangci.yml                # Linter configuration
├── .lefthook.yml                # Pre-commit hook configuration
├── cmd/
│   └── vlt/
│       └── main.go              # Entry point only — wire and run
└── internal/
    ├── crypto/
    │   ├── identity.go          # Passphrase → age identity, memguard zeroing
    │   ├── identity_test.go
    │   ├── vault.go             # Encrypt/decrypt helpers
    │   └── vault_test.go
    ├── store/
    │   ├── secret.go            # Secret types and JSON envelope schema
    │   ├── secret_test.go
    │   ├── store.go             # Directory CRUD operations
    │   └── store_test.go
    ├── ui/
    │   ├── app.go               # Fyne app and window bootstrap
    │   ├── unlock.go            # Passphrase entry screen
    │   ├── list.go              # Secret browser widget (left panel)
    │   ├── detail.go            # Secret view/edit widget (right panel)
    │   └── settings.go          # Settings screen
    └── testutil/
        └── fixtures.go          # Shared test helpers and fake implementations
```

### 2.2 go.mod

```go
module github.com/yourorg/vlt

go 1.23

require (
    filippo.io/age v1.2.1
    fyne.io/fyne/v2 v2.5.3
    github.com/awnumar/memguard v0.22.5
)
```

### 2.3 Makefile

```makefile
.PHONY: build run lint fmt vet test test-race test-cover bench vuln clean package-mac package-win package-linux

BINARY   := vlt
CMD      := ./cmd/vlt
GOFLAGS  := -trimpath
LDFLAGS  := -s -w

build:
    go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

run:
    go run $(CMD)

fmt:
    gofumpt -l -w .
    goimports -l -w .

lint:
    golangci-lint run ./...

vet:
    go vet ./...

test:
    go test ./...

test-race:
    go test -race ./...

test-cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out

bench:
    go test -bench=. -benchmem ./...

vuln:
    govulncheck ./...

check: fmt lint vet test-race vuln  ## Run all checks (CI equivalent)

# Fyne cross-platform packaging (requires fyne CLI: go install fyne.io/fyne/v2/cmd/fyne@latest)
package-mac:
    fyne package -os darwin -icon Icon.png -name "vlt"

package-win:
    fyne package -os windows -icon Icon.png -name "vlt"

package-linux:
    fyne package -os linux -icon Icon.png -name "vlt"

clean:
    rm -f $(BINARY) coverage.out
    rm -rf fyne-cross/
```

### 2.4 golangci-lint Configuration

```yaml
# .golangci.yml
version: "2"

linters:
  enable-all: true
  disable:
    - depguard       # we manage deps manually
    - exhaustruct    # too noisy for UI code
    - gochecknoglobals  # Fyne requires some package-level vars
    - wsl            # formatting — gofumpt handles this

linters-settings:
  govet:
    enable-all: true
  gofumpt:
    module-path: github.com/yourorg/vlt
    extra-rules: true
  gocritic:
    enabled-tags:
      - diagnostic
      - style
      - performance
  gosec:
    excludes:
      - G115  # integer overflow — accept in known-safe contexts

issues:
  exclude-rules:
    - path: "_test.go"
      linters:
        - gosec
        - errcheck
        - dupl
```

### 2.5 lefthook Configuration

```yaml
# .lefthook.yml
pre-commit:
  parallel: true
  commands:
    gofumpt:
      glob: "*.go"
      run: gofumpt -l {staged_files}
    golangci-lint:
      glob: "*.go"
      run: golangci-lint run --fix
    shellcheck:
      glob: "*.sh"
      run: shellcheck {staged_files}
    detect-secrets:
      run: detect-secrets-hook {staged_files}

pre-push:
  commands:
    test-race:
      run: go test -race ./...
    govulncheck:
      run: govulncheck ./...
```

---

# Stage 3: Development

## 3.1 Writing Go Code

### Error Handling Standards

Handle every error. Wrap errors at package boundaries with context:

```go
// Good — wraps error with context, preserves chain
func (s *Store) Get(name string) (*Secret, error) {
    data, err := os.ReadFile(s.secretPath(name))
    if err != nil {
        return nil, fmt.Errorf("store.Get %q: %w", name, err)
    }
    // ...
}

// Good — sentinel errors for conditions the caller might check
var ErrSecretNotFound = errors.New("secret not found")
var ErrVaultLocked   = errors.New("vault is locked")

// Good — custom error type with extra context
type DecryptError struct {
    Name   string
    Reason string
}
func (e *DecryptError) Error() string {
    return fmt.Sprintf("decrypt %q: %s", e.Name, e.Reason)
}

// Bad — discarded error
data, _ := os.ReadFile(path)

// Bad — generic error message with no context
return nil, errors.New("failed")
```

### Type and Struct Patterns

```go
// Named type for domain concepts — prevents stringly-typed APIs
type SecretType string

const (
    TypePassword SecretType = "password"
    TypeRecord   SecretType = "record"
    TypeFile     SecretType = "file"
)

// Immutable value object — use unexported fields + constructor validation
type SecretName struct {
    value string
}

func NewSecretName(s string) (SecretName, error) {
    s = strings.TrimSpace(s)
    if s == "" {
        return SecretName{}, errors.New("secret name cannot be empty")
    }
    if len(s) > 255 {
        return SecretName{}, errors.New("secret name exceeds 255 characters")
    }
    return SecretName{value: s}, nil
}

func (n SecretName) String() string { return n.value }
func (n SecretName) Slug() string   { return slugify(n.value) }
```

### Logging with slog

Use structured logging from the start:

```go
import "log/slog"

// Initialise once in main — JSON for production, text for development
logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))
slog.SetDefault(logger)

// Use structured key-value pairs, never fmt.Sprintf in log calls
slog.Info("secret saved", "name", name, "type", secretType)
slog.Error("decrypt failed", "name", name, "err", err)

// Pass logger explicitly to avoid global state in library code
type Store struct {
    log *slog.Logger
    // ...
}
```

### Configuration

```go
// Load configuration at the boundary (main), pass inward as struct
type Config struct {
    VaultDir      string
    LockTimeoutMs int
    ClipboardTTLs int
}

func ConfigFromEnv() (*Config, error) {
    dir := os.Getenv("VLT_VAULT_DIR")
    if dir == "" {
        var err error
        dir, err = defaultVaultDir()
        if err != nil {
            return nil, fmt.Errorf("config: %w", err)
        }
    }
    return &Config{
        VaultDir:      dir,
        LockTimeoutMs: envIntOrDefault("VLT_LOCK_TIMEOUT_MS", 300_000),
        ClipboardTTLs: envIntOrDefault("VLT_CLIPBOARD_TTL_S", 30),
    }, nil
}
```

### Concurrency Guidelines

- **Context first.** Pass `context.Context` as the first argument to any function that blocks, does I/O, or can be cancelled.
- **Goroutine ownership.** The goroutine that starts a goroutine is responsible for ensuring it terminates.
- **errgroup for structured concurrency.** Use `golang.org/x/sync/errgroup` instead of raw WaitGroup when errors matter.
- **Never close a channel from the reader.** Only the sender closes channels.
- **Protect shared state with sync.Mutex.** Name mutex and the data it protects together; embed immediately above the guarded fields.

```go
import "golang.org/x/sync/errgroup"

func (s *Store) SaveAll(ctx context.Context, secrets []*Secret) error {
    g, ctx := errgroup.WithContext(ctx)
    for _, sec := range secrets {
        sec := sec // capture loop variable (pre-Go 1.22)
        g.Go(func() error {
            return s.Save(ctx, sec)
        })
    }
    return g.Wait()
}
```

### Fyne GUI Guidelines

- **Main goroutine only.** All widget creation and mutation must happen on the main goroutine. Use `fyne.Do(func() { ... })` to dispatch from background goroutines.
- **Data binding.** Use `binding.String`, `binding.Bool`, etc. for reactive UI state rather than manual `Refresh()` calls where possible.
- **Composition over custom widgets.** Reach for `container.NewVBox`, `container.NewHBox`, `container.NewBorder` before writing a custom `fyne.Widget` implementation.
- **Theming.** Use `theme.Color*`, `theme.Text*Size`, `theme.Padding` constants rather than hardcoded values.
- **Custom widgets.** Implement `fyne.Widget` interface: embed `widget.BaseWidget`, implement `CreateRenderer() fyne.WidgetRenderer`.

```go
// Good — background work dispatches back to main goroutine
go func() {
    result, err := store.Get(ctx, name)
    fyne.Do(func() {
        if err != nil {
            dialog.ShowError(err, w)
            return
        }
        detailView.Show(result)
    })
}()

// Bad — widget mutation from background goroutine
go func() {
    result, _ := store.Get(ctx, name)
    label.SetText(result.Name) // DATA RACE — not on main goroutine
}()
```

## 3.2 Git Workflow

### Commit Discipline

- Commits should be atomic: one logical change per commit.
- Write imperative commit messages: `Add vault directory CRUD operations`, not `Added` or `Adds`.
- Use conventional commit prefixes in team contexts: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `ci:`, `chore:`.

### Branch Strategy

| Branch | Purpose |
|--------|---------|
| `main` | Always buildable and passing CI. Protected. |
| `feat/<n>` | New feature work |
| `fix/<n>` | Bug fixes |
| `refactor/<n>` | Internal changes, no behaviour change |

Every branch must pass `make check` before merge.

---

# Stage 4: Testing

## 4.1 Testing Strategy

Follow the **testing pyramid**:

```
         ╱╲
        ╱ E2E ╲          Few — slow, integration, user-facing flows
       ╱────────╲
      ╱Integration╲      Some — filesystem, crypto round-trips
     ╱──────────────╲
    ╱   Unit Tests    ╲   Many — fast, isolated, pure functions
   ╱────────────────────╲
```

### What to Test at Each Level

| Level | Tests | Runs Against | Speed |
|-------|-------|--------------|-------|
| **Unit** | Pure functions, types, domain logic | In-memory only | µs–ms |
| **Integration** | Filesystem CRUD, encrypt/decrypt round-trips | Temp directories | ms |
| **E2E / GUI** | Fyne widget interactions via `fyne/test` | Headless renderer | ms–seconds |

### Test File Organisation

```
internal/
├── crypto/
│   ├── identity.go
│   ├── identity_test.go       # Unit tests alongside source
│   ├── vault.go
│   └── vault_test.go
├── store/
│   ├── store.go
│   └── store_test.go          # Integration — uses t.TempDir()
└── testutil/
    └── fixtures.go            # Shared fakes, builders, helpers
```

## 4.2 Writing Good Tests

### Structure Every Test as Arrange → Act → Assert

```go
func TestStore_Get_ReturnsSecret(t *testing.T) {
    t.Parallel()

    // Arrange
    dir := t.TempDir()
    id  := testutil.NewScryptIdentity(t, "correct-horse-battery-staple")
    s   := store.New(id, dir)
    _ = s.Save(context.Background(), testutil.PasswordSecret("github-key", "ghp_xxx"))

    // Act
    got, err := s.Get(context.Background(), "github-key")

    // Assert
    require.NoError(t, err)
    assert.Equal(t, "github-key", got.Name)
    assert.Equal(t, store.TypePassword, got.Type)
}
```

### Naming Convention

Test names describe **behaviour**, not implementation:

```go
// Good — describes the expected behaviour
func TestStore_Get_ReturnsErrNotFoundForMissingSecret(t *testing.T) { ... }
func TestIdentity_Zero_ClearsBufferContents(t *testing.T)           { ... }
func TestVault_Decrypt_FailsWithWrongPassphrase(t *testing.T)        { ... }

// Bad — describes implementation
func TestGetFunction(t *testing.T)  { ... }
func TestDecryptReturnsTrue(t *testing.T) { ... }
```

### Table-Driven Tests

Use table-driven tests to cover edge cases without duplicating test bodies:

```go
func TestSecretName_Validation(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name    string
        input   string
        wantErr bool
    }{
        {"valid name",      "GitHub API Key",   false},
        {"empty string",    "",                 true},
        {"only whitespace", "   ",              true},
        {"max length",      strings.Repeat("a", 255), false},
        {"over max length", strings.Repeat("a", 256), true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            _, err := store.NewSecretName(tt.input)
            if tt.wantErr {
                require.Error(t, err)
            } else {
                require.NoError(t, err)
            }
        })
    }
}
```

### Property-Based Testing

Use `pgregory.net/rapid` for functions with rich input spaces:

```go
import "pgregory.net/rapid"

func TestVault_EncryptDecryptRoundTrip(t *testing.T) {
    rapid.Check(t, func(rt *rapid.T) {
        passphrase := rapid.StringN(8, 64, -1).Draw(rt, "passphrase")
        plaintext  := rapid.SliceOf(rapid.Byte()).Draw(rt, "plaintext")

        id, err := crypto.NewIdentity(passphrase)
        require.NoError(t, err)
        defer id.Zero()

        ciphertext, err := crypto.Encrypt(id, plaintext)
        require.NoError(t, err)

        got, err := crypto.Decrypt(id, ciphertext)
        require.NoError(t, err)
        assert.Equal(t, plaintext, got)
    })
}
```

### Fyne Widget Testing

Use `fyne.io/fyne/v2/test` for headless GUI tests (no display required):

```go
import (
    "testing"
    "fyne.io/fyne/v2/test"
)

func TestUnlockScreen_ShowsErrorOnWrongPassphrase(t *testing.T) {
    a := test.NewApp()
    defer a.Quit()

    w := a.NewWindow("test")
    screen := ui.NewUnlockScreen(w, fakeVault{failUnlock: true})
    w.SetContent(screen)

    // Simulate user entry
    test.Type(screen.PassphraseEntry(), "wrong-passphrase")
    test.Tap(screen.UnlockButton())

    // Assert error label is visible
    assert.True(t, screen.ErrorLabel().Visible())
    assert.Contains(t, screen.ErrorLabel().Text, "incorrect passphrase")
}
```

### Mocking Guidelines

- **Don't mock what you own.** Test your own code directly; mock only external boundaries.
- **Prefer dependency injection** over monkey patching. Pass collaborators as interface arguments to constructors.
- **Use fakes over mocks** when feasible: an in-memory implementation of an interface is better than a generated mock.
- Avoid `gomock` for most cases — a hand-written fake is clearer and more refactor-stable.

```go
// testutil/fixtures.go — a fake store, not a mock
type FakeStore struct {
    secrets map[string]*store.Secret
    Calls   []string
}

func (f *FakeStore) Get(_ context.Context, name string) (*store.Secret, error) {
    f.Calls = append(f.Calls, "Get:"+name)
    s, ok := f.secrets[name]
    if !ok {
        return nil, store.ErrSecretNotFound
    }
    return s, nil
}
```

## 4.3 Running Tests

```bash
# Fast feedback during development (stop at first failure)
go test -failfast ./...

# With race detector — always in CI
go test -race ./...

# Specific package or test function
go test ./internal/store/... -run TestStore_Get

# Full suite with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out   # open in browser

# Run benchmarks
go test -bench=. -benchmem ./internal/crypto/...

# Verbose output
go test -v ./...

# Skip integration tests (using build tag)
go test -tags '!integration' ./...
```

---

# Stage 5: Verification & Quality Assurance

## 5.1 Static Analysis Pipeline

Run in this order (fastest to slowest):

```bash
# 1. Format check (instant)
gofumpt -l .

# 2. Vet (seconds)
go vet ./...

# 3. Lint (seconds)
golangci-lint run ./...

# 4. Tests with race detector (seconds–minutes)
go test -race ./...

# 5. Vulnerability scan (seconds)
govulncheck ./...
```

Or simply: `make check`

## 5.2 Code Review Checklist

**Correctness**
- [ ] Does it handle all error paths? No `_ =` discarding a meaningful error.
- [ ] Are all edge cases handled? Empty slices, nil pointers, zero values.
- [ ] Are resources cleaned up? Files closed with `defer`, goroutines cancelled.
- [ ] Is there TOCTOU risk in filesystem operations?
- [ ] Does it handle context cancellation correctly?

**Types & Contracts**
- [ ] All exported functions and types have doc comments.
- [ ] Named types used for domain concepts (not raw `string`, `int` for IDs).
- [ ] Interfaces are small and defined at point of use.
- [ ] Error types are exported if callers need to inspect them.

**Security**
- [ ] No secrets or credentials hardcoded or in config files.
- [ ] User input is validated before use (path traversal, injection).
- [ ] Sensitive data is held in `memguard.LockedBuffer`, not plain `[]byte` or `string`.
- [ ] `memguard.LockedBuffer` is zeroed in all exit paths (defer Zero).
- [ ] No use of `math/rand` for security-sensitive random values — use `crypto/rand`.
- [ ] Subprocess calls use `exec.Command` with explicit args, never shell strings.

**Performance**
- [ ] No accidental O(n²) — linear searches on large collections use maps.
- [ ] No goroutine leaks — all goroutines have a termination path.
- [ ] `sync.Pool` used for frequently allocated temporary buffers.
- [ ] `strings.Builder` used for string concatenation in loops, not `+`.

**Maintainability**
- [ ] Functions are ≤40 lines (soft limit — split if exceeded without loss of clarity).
- [ ] Nesting depth ≤3 (use early returns to flatten).
- [ ] Names are descriptive and unambiguous. Avoid single-letter names outside short loops.
- [ ] No commented-out code.
- [ ] `t.Parallel()` in every unit test.

## 5.3 CI Pipeline

```yaml
# .github/workflows/ci.yml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: "1.23"
          cache: true

      - name: Install tools
        run: |
          go install mvdan.cc/gofumpt@latest
          go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
          go install golang.org/x/vuln/cmd/govulncheck@latest

      - name: Format check
        run: gofumpt -l . | grep . && exit 1 || true

      - name: Vet
        run: go vet ./...

      - name: Lint
        run: golangci-lint run ./...

      - name: Test (with race detector)
        run: go test -race -coverprofile=coverage.out ./...

      - name: Vulnerability scan
        run: govulncheck ./...

  build-matrix:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - goos: linux
            goarch: amd64
          - goos: darwin
            goarch: arm64
          - goos: windows
            goarch: amd64
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.23"
          cache: true
      - name: Build
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
        run: go build -trimpath -ldflags="-s -w" ./cmd/vlt
```

## 5.4 Debugging

### Structured Approach

1. **Reproduce**: Write a failing test that demonstrates the bug before fixing anything.
2. **Isolate**: Narrow to the smallest input or state that triggers the issue.
3. **Diagnose**: Use Delve, slog output, or pprof to find the root cause.
4. **Fix**: Make the minimal change that fixes the root cause.
5. **Verify**: The failing test now passes. No other tests broke.
6. **Prevent**: Add the test to the suite permanently. Consider if a type or assertion could prevent recurrence.

### Useful Debugging Techniques

```bash
# Launch Delve debugger
dlv debug ./cmd/vlt

# Attach to running process
dlv attach <PID>

# Run tests under Delve
dlv test ./internal/crypto/...

# CPU profile (add to main or test)
go test -cpuprofile=cpu.prof -bench=BenchmarkDecrypt ./internal/crypto/
go tool pprof -http=:8080 cpu.prof

# Memory profile
go test -memprofile=mem.prof -bench=. ./internal/crypto/
go tool pprof -http=:8080 mem.prof

# Detect race conditions
go test -race ./...

# Execution trace
go test -trace=trace.out ./...
go tool trace trace.out
```

```go
// Quick timing instrumentation (remove before committing)
start := time.Now()
result, err := expensiveOperation(ctx)
slog.Debug("operation complete", "elapsed", time.Since(start))
```

---

# Stage 6: Build & Distribution

## 6.1 Versioning

Use [Semantic Versioning](https://semver.org/):

- **MAJOR**: Breaking API changes
- **MINOR**: New features, backward-compatible
- **PATCH**: Bug fixes, backward-compatible

Embed version at build time:

```go
// cmd/vlt/main.go
var version = "dev" // overridden by ldflags

// Makefile
LDFLAGS := -s -w -X main.version=$(shell git describe --tags --always --dirty)
```

## 6.2 Changelog

Maintain `CHANGELOG.md` using [Keep a Changelog](https://keepachangelog.com/) format:

```markdown
## [Unreleased]

### Added
- Clipboard TTL countdown in status bar

### Fixed
- Auto-lock timer not resetting on keyboard events
```

## 6.3 Building & Packaging

```bash
# Local binary
go build -trimpath -ldflags="-s -w" -o vlt ./cmd/vlt

# Cross-compilation (no CGO required for core; Fyne needs OS-specific packaging)
GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o vlt-linux-amd64 ./cmd/vlt
GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o vlt-darwin-arm64 ./cmd/vlt
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o vlt-windows-amd64.exe ./cmd/vlt

# Fyne native packages (produces .app, .exe with icon, .tar.gz)
fyne package -os darwin  -icon Icon.png -name "vlt" -appID io.yourorg.vlt
fyne package -os windows -icon Icon.png -name "vlt" -appID io.yourorg.vlt
fyne package -os linux   -icon Icon.png -name "vlt" -appID io.yourorg.vlt

# OpenBSD (build on target — no fyne cross-compile support)
GOOS=openbsd GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o vlt ./cmd/vlt
```

> **Note on Fyne cross-compilation:** `fyne package` for non-native targets requires
> [fyne-cross](https://github.com/fyne-io/fyne-cross) which uses Docker containers
> with the appropriate toolchains. Add `fyne-cross` to CI for release builds.

## 6.4 Release Checklist

- [ ] All CI checks pass on `main`
- [ ] Version tag created: `git tag v0.1.0 && git push --tags`
- [ ] `CHANGELOG.md` updated with release date
- [ ] Binaries built for all targets with correct ldflags
- [ ] Fyne packages built for Linux, macOS, Windows
- [ ] GitHub release created with CHANGELOG excerpt and attached binaries
- [ ] `govulncheck` passes on the tagged commit

---

# Quick Reference

## go Commands

| Command | Description |
|---------|-------------|
| `go mod init <module>` | Create new module |
| `go get <pkg>@<version>` | Add or update dependency |
| `go get <pkg>@none` | Remove dependency |
| `go mod tidy` | Remove unused deps, update go.sum |
| `go build ./...` | Build all packages |
| `go run ./cmd/vlt` | Run the application |
| `go test ./...` | Run all tests |
| `go test -race ./...` | Run tests with race detector |
| `go test -bench=. ./...` | Run benchmarks |
| `go vet ./...` | Run static analysis |
| `go generate ./...` | Run code generators |
| `go tool cover -html=coverage.out` | Open coverage report |

## Dependency Management

```bash
# Add a new dependency
go get filippo.io/age@v1.2.1

# Update a dependency to latest
go get -u filippo.io/age

# Update all dependencies (do carefully, then run tests)
go get -u ./...
go mod tidy

# Remove a dependency
go get filippo.io/age@none
go mod tidy

# Check for vulnerabilities
govulncheck ./...

# List all dependencies
go list -m all
```

## Best Practices Checklist

- [ ] `go mod tidy` run and `go.sum` committed
- [ ] `go test -race ./...` passes
- [ ] All exported symbols have doc comments
- [ ] Errors wrapped with `fmt.Errorf("context: %w", err)` at package boundaries
- [ ] Sentinel errors defined as `var Err* = errors.New(...)` (not `const`)
- [ ] No `_ =` discarding errors from meaningful operations
- [ ] `context.Context` passed as first arg to all blocking functions
- [ ] All goroutines have a documented termination path
- [ ] `defer resource.Close()` immediately after every resource acquisition
- [ ] `memguard.LockedBuffer` used for all passphrase/key material; `defer Zero()` in all paths
- [ ] `crypto/rand` used for all security-relevant random values
- [ ] `time.Now().UTC()` used everywhere — never bare `time.Now()`
- [ ] `slog` used for all logging — never `fmt.Println` in committed code
- [ ] `t.Parallel()` in every unit test
- [ ] Table-driven tests for functions with multiple input cases
- [ ] `golangci-lint` passes with no suppressions that aren't documented
- [ ] `govulncheck` passes in CI
- [ ] Fyne widget mutations only on main goroutine (use `fyne.Do()`)
- [ ] Build matrix CI covers Linux, macOS, Windows

