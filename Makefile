BINARY := san
BINDIR := bin
SRCDIR := ./cmd/san
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

# Disable cgo so binaries are statically linked, with no glibc version
# dependency that would break on older distros.
export CGO_ENABLED := 0
GOFILES := $(shell find . -path './vendor' -prune -o -path './.git' -prune -o -name '*.go' -print)
GOIMPORTS_VERSION := v0.43.0

.PHONY: build build-all install clean release release-push test cover format format-check lint install-format-tools check-format-tools

build: format
	@mkdir -p $(BINDIR)
	go build $(LDFLAGS) -o $(BINDIR)/$(BINARY) $(SRCDIR)

build-all: format
	go build ./...

install: build
	@mkdir -p $(HOME)/.local/bin
	cp $(BINDIR)/$(BINARY) $(HOME)/.local/bin/

install-format-tools:
	go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

check-format-tools:
	@command -v goimports >/dev/null || go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

format: check-format-tools
	@gofmt -w $(GOFILES)
	@goimports -w $(GOFILES)

format-check: check-format-tools
	@files="$$(gofmt -l $(GOFILES))"; \
	if [ -n "$$files" ]; then \
		echo "Go files are not formatted. Run: make format"; \
		echo "$$files"; \
		exit 1; \
	fi
	@files="$$(goimports -l $(GOFILES))"; \
	if [ -n "$$files" ]; then \
		echo "Go imports are not formatted. Run: make format"; \
		echo "$$files"; \
		exit 1; \
	fi

lint:
	go vet ./...
	@$(MAKE) format-check
	@$(MAKE) lint-layers

lint-layers:
	@go run ./tools/layercheck

test:
	go test ./...

# cover runs the unit tests with the race detector and writes an atomic
# coverage profile (coverage.out) for upload to Codecov. covermode=atomic
# is required when -race is enabled.
cover:
	go test -race -covermode=atomic -coverprofile=coverage.out ./internal/...

# ci runs everything the GitHub workflow runs, in the same order. Use
# `make ci` before pushing to catch format / vet / layercheck / test
# failures locally instead of round-tripping through Actions.
ci: format-check build-all lint
	$(MAKE) cover
	go test ./tests/integration/...

clean:
	rm -rf $(BINDIR)

release: format
	@mkdir -p $(BINDIR)
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BINDIR)/$(BINARY)_darwin_amd64 $(SRCDIR)
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINDIR)/$(BINARY)_darwin_arm64 $(SRCDIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINDIR)/$(BINARY)_linux_amd64 $(SRCDIR)
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINDIR)/$(BINARY)_linux_arm64 $(SRCDIR)
	cd $(BINDIR) && cp $(BINARY)_darwin_amd64 $(BINARY) && tar -czf $(BINARY)_darwin_amd64.tar.gz $(BINARY) && rm $(BINARY)
	cd $(BINDIR) && cp $(BINARY)_darwin_arm64 $(BINARY) && tar -czf $(BINARY)_darwin_arm64.tar.gz $(BINARY) && rm $(BINARY)
	cd $(BINDIR) && cp $(BINARY)_linux_amd64 $(BINARY) && tar -czf $(BINARY)_linux_amd64.tar.gz $(BINARY) && rm $(BINARY)
	cd $(BINDIR) && cp $(BINARY)_linux_arm64 $(BINARY) && tar -czf $(BINARY)_linux_arm64.tar.gz $(BINARY) && rm $(BINARY)

release-push:
	@test -n "$(VERSION)" || { echo "VERSION is required, e.g. make release-push VERSION=v1.15.2"; exit 1; }
	@case "$(VERSION)" in v*) tag="$(VERSION)" ;; *) tag="v$(VERSION)" ;; esac; \
	git diff --quiet || { echo "working tree is not clean"; exit 1; }; \
	git diff --cached --quiet || { echo "index has staged changes"; exit 1; }; \
	git rev-parse --verify "$$tag" >/dev/null 2>&1 && { echo "tag $$tag already exists"; exit 1; }; \
	grep -q "^## \[$$tag\]" CHANGELOG.md || { echo "CHANGELOG.md is missing section $$tag"; exit 1; }; \
	git push origin main && git tag "$$tag" && git push origin "$$tag"
