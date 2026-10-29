.PHONY: build run lint fmt vet test test-race test-cover bench vuln clean package-mac package-linux check screenshots

BINARY   := vlt
CMD      := ./cmd/vlt
GOFLAGS  := -trimpath
# The version shown in the window title and logs: the nearest tag, or the
# commit hash, with -dirty for uncommitted changes.
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

run:
	go run $(CMD)

fmt:  ## Format with the formatters configured in .golangci.yml (gofumpt, goimports)
	golangci-lint fmt ./...

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

check: fmt lint vet test-race vuln  ## Format, then run the checks CI runs

# Packaging (see packaging/). APP_ID is also set in cmd/vlt/main.go.
APP_ID      := io.yourorg.vlt
# Package file names carry the full version: the tag on a release
# (v0.1.0), or tag, commit count and hash in between (v0.1.0-5-gabc1234).
PKG_VERSION := $(shell git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)
# macOS bundle versions must be plain x.y.z: the nearest such tag (not a
# pre-release like v1.2.0-rc1), else
# 0.0.0 before the first release. The build number (commit count) tells
# builds apart.
APP_VERSION := $(or $(shell git describe --tags --match 'v[0-9]*' --exclude '*-*' --abbrev=0 2>/dev/null | sed -n 's/^v\([0-9]*\.[0-9]*\.[0-9]*\)$$/\1/p'),0.0.0)
APP_BUILD   := $(shell git rev-list --count HEAD 2>/dev/null || echo 1)

# The oldest macOS the app supports: Go 1.27's minimum. cgo's linker would
# otherwise target the build machine's macOS; CGO_* also keys the build cache.
MACOS_MIN := 13.0
ifeq ($(shell uname -s),Darwin)
# The flag is appended, so CGO_CFLAGS set in the environment keeps it.
CGO_CFLAGS ?= -O2 -g
export MACOSX_DEPLOYMENT_TARGET := $(MACOS_MIN)
export CGO_CFLAGS  += -mmacosx-version-min=$(MACOS_MIN)
export CGO_LDFLAGS += -mmacosx-version-min=$(MACOS_MIN)
# Fyne's cgo packages each link -lobjc, and Apple's linker (Xcode 15+) warns
# "ignoring duplicate libraries". The warning is harmless; silence it when
# the linker knows the flag (older linkers reject it).
ifeq ($(shell printf 'int main(void){return 0;}' | cc -x c - -o /dev/null -Wl,-no_warn_duplicate_libraries 2>/dev/null && echo yes),yes)
export CGO_LDFLAGS += -Wl,-no_warn_duplicate_libraries
endif
endif

# vlt.app and dist/vlt-<version>-macos-<arch>.zip, ad-hoc signed; on a Mac.
package-mac: build
	packaging/macos/build-app.sh $(BINARY) $(APP_ID) $(APP_VERSION) $(APP_BUILD) $(MACOS_MIN) dist/vlt-$(PKG_VERSION)-macos-$(shell uname -m).zip

# dist/vlt-<version>-x86_64.AppImage; on Linux (CI builds it on the oldest
# supported Ubuntu, see .github/workflows/ci.yml). Needs curl and sha256sum.
package-linux: build
	packaging/linux/build-appimage.sh $(BINARY) $(PKG_VERSION) dist

screenshots:  ## Render the README screenshots into docs/screenshots (no display needed)
	VLT_SCREENSHOTS=$(CURDIR)/docs/screenshots go test ./internal/ui -run '^TestScreenshots$$' -count=1

clean:
	rm -f $(BINARY) coverage.out
	rm -rf vlt.app dist/
