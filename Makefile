VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
BUILD_TIME_UTC := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GIT_COMMIT := $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.gitCommit=$(GIT_COMMIT) \
	-X main.buildTimeUTC=$(BUILD_TIME_UTC)
GOEXE := $(shell go env GOEXE)
# One pin for the Makefile and the CI lint job; see .github/workflows/ci.yml.
GOLANGCI_VERSION := $(shell cat .golangci-version)
WAILS_VERSION := $(shell tr -d '[:space:]' < .wails-version)

.PHONY: build vet fmt lint lint-go lint-install lint-cross lint-update wails-install test test-triage desktop-test desktop-test-short desktop-test-times desktop-test-triage sdk-test sdk-test-race hooks cross clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/reasonix$(GOEXE) ./cmd/reasonix
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/reasonix-plugin-example$(GOEXE) ./cmd/reasonix-plugin-example

vet:
	go vet ./...

fmt:
	gofmt -w .

# Both gates CI runs, at the version CI pins. Skipping golangci-lint locally
# trades a second here for a ten-minute CI round trip: `modernize` findings in
# particular never surface in `go vet`.
lint: lint-go
	go run ./tools/repolint
	node scripts/check-architecture.mjs
	node scripts/check-injection-entrypoints.mjs
	bash scripts/check-wails-pin.sh
	bash scripts/check-wails-pin.test.sh

lint-go:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not installed; run: make lint-install"; exit 1; }
	@have=$$(golangci-lint version --short 2>/dev/null); want=$$(echo "$(GOLANGCI_VERSION)" | sed 's/^v//'); \
		[ "$$have" = "$$want" ] || echo "warning: local golangci-lint $$have, CI pins $$want (make lint-install)"
	golangci-lint run --timeout=5m ./...

# CGO_ENABLED=0 keeps the install working where a stray clang on PATH shadows
# the toolchain and breaks runtime/cgo.
lint-install:
	CGO_ENABLED=0 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

lint-update:
	go run ./tools/repolint -update

wails-install:
	bash scripts/check-wails-pin.sh
	go install "github.com/wailsapp/wails/v2/cmd/wails@$(WAILS_VERSION)"

# Linting one GOOS leaves every //go:build windows and darwin file unchecked.
lint-cross:
	@for t in "linux ." "darwin ." "windows ." "linux desktop" "windows desktop"; do \
		set -- $$t; \
		echo "== golangci-lint GOOS=$$1 ($$2)"; \
		(cd $$2 && GOOS=$$1 golangci-lint run --timeout=5m ./...) || exit 1; \
	done

# -timeout 30m: go's per-package default is 10m, but the agent package alone
# has measured at 750s+ and the desktop package at 1203s on this repo, so the
# default turns slow-but-healthy packages into "panic: test timed out after
# 10m" environment false positives (tl1788 pattern 4). 30m keeps the ceiling
# meaningful while leaving real hangs to the caller's own kill switch.
GO_TEST_TIMEOUT := 30m

test:
	go test -timeout $(GO_TEST_TIMEOUT) ./...

# tl1788 pattern 5: same package sets, but every failing package gets
# isolated re-runs (default 3 attempts) so load-sensitive flaky tests
# (TestConnectionReuseBaseline / CountSentFromToday family / BindingReaders)
# classify as FLAKY instead of forcing a manual single-run triage round.
test-triage:
	bash scripts/go-test-triage.sh ./...

desktop-test:
	cd desktop && go test -timeout $(GO_TEST_TIMEOUT) .

desktop-test-short:
	cd desktop && go test -timeout $(GO_TEST_TIMEOUT) -short .

desktop-test-times:
	cd desktop && go test -timeout $(GO_TEST_TIMEOUT) -count=1 -json . | python3 ../scripts/desktop-test-times.py

desktop-test-triage:
	cd desktop && bash ../scripts/go-test-triage.sh .

sdk-test:
	cd sdk/go && go test -timeout $(GO_TEST_TIMEOUT) ./...

sdk-test-race:
	cd sdk/go && go test -timeout $(GO_TEST_TIMEOUT) -race ./...

sdk-test-triage:
	cd sdk/go && bash ../../scripts/go-test-triage.sh ./...

hooks:
	@git config core.hooksPath .githooks
	@echo "installed: core.hooksPath -> .githooks (pre-push runs go vet)"

cross:
	@mkdir -p dist
	@for p in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "build $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/reasonix-$$os-$$arch$$ext ./cmd/reasonix; \
	done

clean:
	rm -rf bin dist
