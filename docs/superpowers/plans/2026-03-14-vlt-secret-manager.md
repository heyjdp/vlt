# vlt — Age-based Secret Manager Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a cross-platform GUI secret manager using age encryption, Fyne GUI, and memguard secure memory.

**Architecture:** Layered architecture with domain layer (crypto, store) having zero external dependencies on the UI. Infrastructure adapters implement interfaces defined by the domain. The Fyne GUI is a thin interface layer that delegates to the application/domain layers.

**Tech Stack:** Go 1.23+, filippo.io/age (encryption), fyne.io/fyne/v2 (GUI), github.com/awnumar/memguard (secure memory), log/slog (logging)

---

## Chunk 1: Project Setup and Crypto Package

### Task 1: Install Go Toolchain

- [ ] **Step 1: Install Go via Homebrew**
```bash
brew install go
```

- [ ] **Step 2: Verify Go installation**
```bash
go version
# Expected: go version go1.23.x darwin/arm64
```

### Task 2: Initialize Project Structure

**Files:**
- Create: `go.mod`
- Create: `cmd/vlt/main.go`
- Create: `Makefile`
- Create: `.golangci.yml`
- Create: `.lefthook.yml`
- Create: `internal/crypto/`, `internal/store/`, `internal/ui/`, `internal/testutil/`

- [ ] **Step 1: Initialize go module**
```bash
mkdir vlt && cd vlt
go mod init github.com/heyjdp/vlt
```

- [ ] **Step 2: Add core dependencies**
```bash
go get filippo.io/age
go get fyne.io/fyne/v2
go get github.com/awnumar/memguard
go mod tidy
```

- [ ] **Step 3: Create directory structure**
```bash
mkdir -p cmd/vlt internal/crypto internal/store internal/ui internal/testutil .github/workflows
```

- [ ] **Step 4: Create Makefile**
See CLAUDE.md §2.3 for full content.

- [ ] **Step 5: Create .golangci.yml**
See CLAUDE.md §2.4 for full content.

- [ ] **Step 6: Create .lefthook.yml**
See CLAUDE.md §2.5 for full content.

- [ ] **Step 7: Create minimal main.go**
```go
// cmd/vlt/main.go
package main

import "fmt"

var version = "dev"

func main() {
    fmt.Println("vlt", version)
}
```

- [ ] **Step 8: Verify build**
```bash
go build -trimpath -ldflags="-s -w" -o vlt ./cmd/vlt
./vlt
# Expected: "vlt dev"
```

- [ ] **Step 9: Commit**
```bash
git init
git add -A
git commit -m "feat: initialize project structure with go.mod and tooling config"
```

### Task 3: Implement internal/crypto Package (TDD)

**Files:**
- Create: `internal/crypto/identity.go`
- Create: `internal/crypto/identity_test.go`
- Create: `internal/crypto/vault.go`
- Create: `internal/crypto/vault_test.go`

#### 3a: Identity — passphrase to age identity with memguard

- [ ] **Step 1: Write failing test for NewIdentity**
```go
// internal/crypto/identity_test.go
package crypto_test

import (
    "testing"

    "github.com/heyjdp/vlt/internal/crypto"
)

func TestNewIdentity_CreatesIdentityFromPassphrase(t *testing.T) {
    t.Parallel()

    id, err := crypto.NewIdentity("correct-horse-battery-staple")
    if err != nil {
        t.Fatalf("NewIdentity returned error: %v", err)
    }
    defer id.Zero()

    if id == nil {
        t.Fatal("expected non-nil identity")
    }
}

func TestNewIdentity_RejectsEmptyPassphrase(t *testing.T) {
    t.Parallel()

    _, err := crypto.NewIdentity("")
    if err == nil {
        t.Fatal("expected error for empty passphrase")
    }
}

func TestIdentity_Zero_ClearsBuffer(t *testing.T) {
    t.Parallel()

    id, err := crypto.NewIdentity("test-passphrase")
    if err != nil {
        t.Fatalf("NewIdentity returned error: %v", err)
    }

    id.Zero()
    // After zeroing, the identity should not be usable
    if !id.IsZeroed() {
        t.Fatal("expected identity to be zeroed")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**
```bash
go test ./internal/crypto/... -v
# Expected: FAIL — package/types not defined
```

- [ ] **Step 3: Write identity.go implementation**
```go
// internal/crypto/identity.go
package crypto

import (
    "errors"
    "fmt"
    "sync"

    "filippo.io/age"
    "github.com/awnumar/memguard"
)

// Identity holds a derived age scrypt identity in a secure memory buffer.
// Call Zero() to wipe the identity from memory when no longer needed.
type Identity struct {
    mu       sync.Mutex
    buf      *memguard.LockedBuffer
    identity *age.ScryptIdentity
    zeroed   bool
}

// NewIdentity derives an age ScryptIdentity from a passphrase.
// The passphrase is stored in a memguard LockedBuffer (mlock'd with guard pages).
// Caller must call Zero() when finished.
func NewIdentity(passphrase string) (*Identity, error) {
    if passphrase == "" {
        return nil, errors.New("crypto.NewIdentity: passphrase cannot be empty")
    }

    // Store passphrase in secure memory
    buf := memguard.NewBufferFromBytes([]byte(passphrase))

    identity, err := age.NewScryptIdentity(string(buf.Bytes()))
    if err != nil {
        buf.Destroy()
        return nil, fmt.Errorf("crypto.NewIdentity: %w", err)
    }

    return &Identity{
        buf:      buf,
        identity: identity,
    }, nil
}

// ScryptIdentity returns the underlying age identity for use in encrypt/decrypt.
// Returns an error if the identity has been zeroed.
func (id *Identity) ScryptIdentity() (*age.ScryptIdentity, error) {
    id.mu.Lock()
    defer id.mu.Unlock()

    if id.zeroed {
        return nil, errors.New("crypto.Identity: identity has been zeroed")
    }
    return id.identity, nil
}

// Recipient returns an age ScryptRecipient for encrypting to this identity.
func (id *Identity) Recipient() (*age.ScryptRecipient, error) {
    id.mu.Lock()
    defer id.mu.Unlock()

    if id.zeroed {
        return nil, errors.New("crypto.Identity: identity has been zeroed")
    }

    r, err := age.NewScryptRecipient(string(id.buf.Bytes()))
    if err != nil {
        return nil, fmt.Errorf("crypto.Identity.Recipient: %w", err)
    }
    return r, nil
}

// Zero destroys the secure buffer and marks the identity as unusable.
func (id *Identity) Zero() {
    id.mu.Lock()
    defer id.mu.Unlock()

    if !id.zeroed {
        id.buf.Destroy()
        id.identity = nil
        id.zeroed = true
    }
}

// IsZeroed reports whether the identity has been zeroed.
func (id *Identity) IsZeroed() bool {
    id.mu.Lock()
    defer id.mu.Unlock()
    return id.zeroed
}
```

- [ ] **Step 4: Run tests to verify they pass**
```bash
go test ./internal/crypto/... -v
# Expected: PASS
```

- [ ] **Step 5: Commit**
```bash
git add internal/crypto/identity.go internal/crypto/identity_test.go
git commit -m "feat: add crypto identity with memguard secure storage"
```

#### 3b: Vault — encrypt/decrypt helpers

- [ ] **Step 6: Write failing test for Encrypt/Decrypt round-trip**
```go
// internal/crypto/vault_test.go
package crypto_test

import (
    "testing"

    "github.com/heyjdp/vlt/internal/crypto"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
    t.Parallel()

    id, err := crypto.NewIdentity("test-passphrase-encrypt")
    if err != nil {
        t.Fatalf("NewIdentity: %v", err)
    }
    defer id.Zero()

    plaintext := []byte(`{"type":"password","name":"test","value":"secret123"}`)

    ciphertext, err := crypto.Encrypt(id, plaintext)
    if err != nil {
        t.Fatalf("Encrypt: %v", err)
    }

    if len(ciphertext) == 0 {
        t.Fatal("expected non-empty ciphertext")
    }

    got, err := crypto.Decrypt(id, ciphertext)
    if err != nil {
        t.Fatalf("Decrypt: %v", err)
    }

    if string(got) != string(plaintext) {
        t.Fatalf("round-trip mismatch: got %q, want %q", got, plaintext)
    }
}

func TestDecrypt_FailsWithWrongPassphrase(t *testing.T) {
    t.Parallel()

    id1, err := crypto.NewIdentity("passphrase-one")
    if err != nil {
        t.Fatalf("NewIdentity: %v", err)
    }
    defer id1.Zero()

    id2, err := crypto.NewIdentity("passphrase-two")
    if err != nil {
        t.Fatalf("NewIdentity: %v", err)
    }
    defer id2.Zero()

    plaintext := []byte("secret data")

    ciphertext, err := crypto.Encrypt(id1, plaintext)
    if err != nil {
        t.Fatalf("Encrypt: %v", err)
    }

    _, err = crypto.Decrypt(id2, ciphertext)
    if err == nil {
        t.Fatal("expected error decrypting with wrong passphrase")
    }
}

func TestEncrypt_FailsWithZeroedIdentity(t *testing.T) {
    t.Parallel()

    id, err := crypto.NewIdentity("test-passphrase")
    if err != nil {
        t.Fatalf("NewIdentity: %v", err)
    }
    id.Zero()

    _, err = crypto.Encrypt(id, []byte("data"))
    if err == nil {
        t.Fatal("expected error encrypting with zeroed identity")
    }
}
```

- [ ] **Step 7: Run test to verify it fails**
```bash
go test ./internal/crypto/... -v -run TestEncrypt
# Expected: FAIL — Encrypt/Decrypt not defined
```

- [ ] **Step 8: Write vault.go implementation**
```go
// internal/crypto/vault.go
package crypto

import (
    "bytes"
    "fmt"
    "io"

    "filippo.io/age"
)

// Encrypt encrypts plaintext using the identity's scrypt recipient.
// Returns the age-encrypted ciphertext.
func Encrypt(id *Identity, plaintext []byte) ([]byte, error) {
    recipient, err := id.Recipient()
    if err != nil {
        return nil, fmt.Errorf("crypto.Encrypt: %w", err)
    }

    var buf bytes.Buffer
    w, err := age.Encrypt(&buf, recipient)
    if err != nil {
        return nil, fmt.Errorf("crypto.Encrypt: %w", err)
    }

    if _, err := w.Write(plaintext); err != nil {
        return nil, fmt.Errorf("crypto.Encrypt: write: %w", err)
    }

    if err := w.Close(); err != nil {
        return nil, fmt.Errorf("crypto.Encrypt: close: %w", err)
    }

    return buf.Bytes(), nil
}

// Decrypt decrypts age-encrypted ciphertext using the identity.
// Returns the original plaintext.
func Decrypt(id *Identity, ciphertext []byte) ([]byte, error) {
    identity, err := id.ScryptIdentity()
    if err != nil {
        return nil, fmt.Errorf("crypto.Decrypt: %w", err)
    }

    r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
    if err != nil {
        return nil, fmt.Errorf("crypto.Decrypt: %w", err)
    }

    plaintext, err := io.ReadAll(r)
    if err != nil {
        return nil, fmt.Errorf("crypto.Decrypt: read: %w", err)
    }

    return plaintext, nil
}
```

- [ ] **Step 9: Run all crypto tests**
```bash
go test ./internal/crypto/... -v
# Expected: all PASS
```

- [ ] **Step 10: Commit**
```bash
git add internal/crypto/vault.go internal/crypto/vault_test.go
git commit -m "feat: add age encrypt/decrypt helpers"
```

---

## Chunk 2: Store Package

### Task 4: Implement internal/store Package (TDD)

**Files:**
- Create: `internal/store/secret.go`
- Create: `internal/store/secret_test.go`
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`
- Create: `internal/testutil/fixtures.go`

#### 4a: Secret types and JSON envelope

- [ ] **Step 1: Write failing test for secret types**
```go
// internal/store/secret_test.go
package store_test

import (
    "encoding/json"
    "strings"
    "testing"
    "time"

    "github.com/heyjdp/vlt/internal/store"
)

func TestSecretType_Constants(t *testing.T) {
    t.Parallel()

    if store.TypePassword != "password" {
        t.Fatalf("expected %q, got %q", "password", store.TypePassword)
    }
    if store.TypeRecord != "record" {
        t.Fatalf("expected %q, got %q", "record", store.TypeRecord)
    }
    if store.TypeFile != "file" {
        t.Fatalf("expected %q, got %q", "file", store.TypeFile)
    }
}

func TestSecret_MarshalJSON_Password(t *testing.T) {
    t.Parallel()

    now := time.Now().UTC()
    s := &store.Secret{
        Version:    1,
        Type:       store.TypePassword,
        Name:       "GitHub API Key",
        CreatedAt:  now,
        ModifiedAt: now,
        Tags:       []string{"github", "ci"},
        Notes:      "Expires 2027-03-14",
        Value:      "ghp_xxxxxxxxxxxx",
    }

    data, err := json.Marshal(s)
    if err != nil {
        t.Fatalf("Marshal: %v", err)
    }

    var got store.Secret
    if err := json.Unmarshal(data, &got); err != nil {
        t.Fatalf("Unmarshal: %v", err)
    }

    if got.Name != s.Name {
        t.Fatalf("name mismatch: got %q, want %q", got.Name, s.Name)
    }
    if got.Value != s.Value {
        t.Fatalf("value mismatch: got %q, want %q", got.Value, s.Value)
    }
}

func TestSlugify(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name string
        input string
        want string
    }{
        {"simple", "GitHub API Key", "github-api-key"},
        {"special chars", "my@secret!val", "my-secret-val"},
        {"spaces", "  lots   of   spaces  ", "lots-of-spaces"},
        {"unicode", "café-token", "caf-token"},
        {"dashes", "already-slugged", "already-slugged"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            got := store.Slugify(tt.input)
            if got != tt.want {
                t.Fatalf("Slugify(%q) = %q, want %q", tt.input, got, tt.want)
            }
        })
    }
}

func TestNewSecretName_Validation(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name    string
        input   string
        wantErr bool
    }{
        {"valid name", "GitHub API Key", false},
        {"empty string", "", true},
        {"only whitespace", "   ", true},
        {"max length", strings.Repeat("a", 255), false},
        {"over max length", strings.Repeat("a", 256), true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            _, err := store.NewSecretName(tt.input)
            if tt.wantErr && err == nil {
                t.Fatal("expected error")
            }
            if !tt.wantErr && err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
        })
    }
}
```

- [ ] **Step 2: Run test to verify it fails**
```bash
go test ./internal/store/... -v
# Expected: FAIL — types not defined
```

- [ ] **Step 3: Write secret.go implementation**
(See plan for full implementation with Secret struct, SecretType, SecretName, Slugify, JSON marshalling)

- [ ] **Step 4: Run tests**
```bash
go test ./internal/store/... -v
# Expected: PASS
```

- [ ] **Step 5: Commit**
```bash
git add internal/store/secret.go internal/store/secret_test.go
git commit -m "feat: add secret types and JSON envelope schema"
```

#### 4b: Store CRUD operations

- [ ] **Step 6: Write testutil fixtures**
```go
// internal/testutil/fixtures.go
package testutil

import (
    "testing"
    "time"

    "github.com/heyjdp/vlt/internal/crypto"
    "github.com/heyjdp/vlt/internal/store"
)

// NewTestIdentity creates a crypto.Identity for testing.
func NewTestIdentity(t *testing.T, passphrase string) *crypto.Identity {
    t.Helper()
    id, err := crypto.NewIdentity(passphrase)
    if err != nil {
        t.Fatalf("testutil.NewTestIdentity: %v", err)
    }
    t.Cleanup(func() { id.Zero() })
    return id
}

// PasswordSecret creates a test password secret.
func PasswordSecret(name, value string) *store.Secret {
    now := time.Now().UTC()
    return &store.Secret{
        Version:    1,
        Type:       store.TypePassword,
        Name:       name,
        CreatedAt:  now,
        ModifiedAt: now,
        Value:      value,
    }
}

// RecordSecret creates a test record secret.
func RecordSecret(name, username, password, url string) *store.Secret {
    now := time.Now().UTC()
    return &store.Secret{
        Version:    1,
        Type:       store.TypeRecord,
        Name:       name,
        CreatedAt:  now,
        ModifiedAt: now,
        Username:   username,
        Password:   password,
        URL:        url,
    }
}
```

- [ ] **Step 7: Write failing store CRUD tests**
(Tests for Store.Save, Store.Get, Store.List, Store.Delete using t.TempDir())

- [ ] **Step 8: Write store.go implementation**
(Store struct with crypto.Identity, vault dir; CRUD methods that encrypt/decrypt via crypto package)

- [ ] **Step 9: Run all store tests**
```bash
go test ./internal/store/... -v
# Expected: PASS
```

- [ ] **Step 10: Run all tests with race detector**
```bash
go test -race ./...
# Expected: PASS
```

- [ ] **Step 11: Commit**
```bash
git add internal/store/store.go internal/store/store_test.go internal/testutil/fixtures.go
git commit -m "feat: add store CRUD with encrypt/decrypt on vault directory"
```

---

## Chunk 3: GUI Implementation

### Task 5: Create cmd/vlt/main.go with Fyne Bootstrap

**Files:**
- Modify: `cmd/vlt/main.go`

- [ ] **Step 1: Update main.go with Fyne app, config, and logging**
- [ ] **Step 2: Verify it builds and runs**
- [ ] **Step 3: Commit**

### Task 6: Unlock Screen and App Shell

**Files:**
- Create: `internal/ui/app.go`
- Create: `internal/ui/unlock.go`

- [ ] **Step 1: Write ui/app.go — app state management, lock/unlock transitions**
- [ ] **Step 2: Write ui/unlock.go — passphrase entry with error display**
- [ ] **Step 3: Wire into main.go**
- [ ] **Step 4: Manual test — app launches, shows unlock screen**
- [ ] **Step 5: Commit**

### Task 7: Secret List and Detail Views

**Files:**
- Create: `internal/ui/list.go`
- Create: `internal/ui/detail.go`

- [ ] **Step 1: Write ui/list.go — category tree, secret list, search filter**
- [ ] **Step 2: Write ui/detail.go — password/record/file forms, clipboard copy**
- [ ] **Step 3: Wire list selection → detail view in app.go**
- [ ] **Step 4: Add create/edit/delete flows**
- [ ] **Step 5: Manual test — full CRUD workflow**
- [ ] **Step 6: Commit**

### Task 8: File Import/Export, Settings, Polish

**Files:**
- Create: `internal/ui/settings.go`
- Modify: `internal/ui/app.go`
- Modify: `internal/ui/detail.go`

- [ ] **Step 1: Add file import (file picker → base64 → encrypt → save)**
- [ ] **Step 2: Add file export (decrypt → base64 decode → save dialog)**
- [ ] **Step 3: Add MIME detection and 10MB limit**
- [ ] **Step 4: Write ui/settings.go — vault path, lock timeout, clipboard TTL**
- [ ] **Step 5: Add clipboard TTL with status bar countdown**
- [ ] **Step 6: Add auto-lock with inactivity timer**
- [ ] **Step 7: Add search/filter in list view**
- [ ] **Step 8: Commit**

### Task 9: CI Pipeline and Final Verification

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create CI workflow per CLAUDE.md §5.3**
- [ ] **Step 2: Run `make check` locally**
- [ ] **Step 3: Verify all tests pass with race detector**
- [ ] **Step 4: Commit**
