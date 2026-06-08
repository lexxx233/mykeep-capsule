# mykeep — pure-Go, no-CGo, USB-portable encrypted memory server.
VERSION ?= 0.1.0-dev
LDFLAGS := -s -w -X main.version=$(VERSION)
GO ?= go
export CGO_ENABLED=0

PLATFORMS := windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build test race vet cross dist guard clean

build:
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o bin/mykeep ./cmd/mykeep

test:
	$(GO) test ./...

race:
	CGO_ENABLED=1 $(GO) test -race ./internal/store/...

vet:
	$(GO) vet ./...

# guard: prove the default build pulls in zero CGo (PLAN §10.1).
guard:
	CC=/bin/false CGO_ENABLED=0 $(GO) build ./... && echo "no-cgo build OK"
	@bad=$$($(GO) list -deps -f '{{.ImportPath}} {{.CgoFiles}}' ./cmd/mykeep | grep -v '\[\]' || true); \
	if [ -n "$$bad" ]; then echo "CGo files found:"; echo "$$bad"; exit 1; else echo "no CgoFiles in dependency graph"; fi

# cross: build all six platform binaries, flat, at the drive root (dist/mykeep/).
cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "  $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags="$(LDFLAGS)" \
			-o dist/mykeep/mykeep-$$os-$$arch$$ext ./cmd/mykeep || exit 1; \
	done

# dist: assemble the USB drive layout — six platform binaries + thin launchers at
# the drive root; the binary creates mykeep_kb/ beside itself on first launch.
dist: cross
	@mkdir -p dist/mykeep
	@printf '@echo off\r\nset A=%%PROCESSOR_ARCHITECTURE%%\r\nif /I "%%PROCESSOR_ARCHITEW6432%%"=="ARM64" set A=ARM64\r\nif /I "%%A%%"=="ARM64" ("%%~dp0mykeep-windows-arm64.exe" %%*) else ("%%~dp0mykeep-windows-amd64.exe" %%*)\r\n' > dist/mykeep/mykeep.cmd
	@printf '#!/bin/sh\nDIR="$$(cd "$$(dirname "$$0")" && pwd)"\nexec "$$DIR/mykeep-darwin-$$(uname -m | sed s/x86_64/amd64/)" "$$@"\n' > dist/mykeep/mykeep.command
	@printf '#!/bin/sh\nDIR="$$(cd "$$(dirname "$$0")" && pwd)"\nexec "$$DIR/mykeep-linux-$$(uname -m | sed s/x86_64/amd64/ | sed s/aarch64/arm64/)" "$$@"\n' > dist/mykeep/mykeep.sh
	@chmod +x dist/mykeep/mykeep.command dist/mykeep/mykeep.sh
	@echo "drive layout in dist/mykeep/"

clean:
	rm -rf bin dist
