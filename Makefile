.PHONY: build run lint fmt vet test test-race test-cover bench vuln clean package-mac package-win package-linux check

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
