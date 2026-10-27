.PHONY: build run lint fmt vet test test-race test-cover bench vuln clean package-mac package-win package-linux check

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

# Fyne packaging for the host OS (requires the fyne CLI: go install fyne.io/fyne/v2/cmd/fyne@latest);
# packaging for another OS needs fyne-cross
package-mac:
	fyne package -os darwin -icon Icon.png -name "vlt"

package-win:
	fyne package -os windows -icon Icon.png -name "vlt"

package-linux:
	fyne package -os linux -icon Icon.png -name "vlt"

clean:
	rm -f $(BINARY) coverage.out
	rm -rf fyne-cross/
