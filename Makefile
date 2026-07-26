PLUGIN_NAME := model-mapper-plus
DIST_DIR := dist
GO ?= go
GOOS ?=
GOARCH ?=
BUILD_CC ?=
VERSION ?=
LDFLAGS ?= -s -w
VERSION_LDFLAGS := $(if $(VERSION),-X main.pluginVersion=$(VERSION),)
WINDOWS_AMD64_OUT := $(DIST_DIR)/windows_amd64/$(PLUGIN_NAME).dll
LINUX_AMD64_OUT := $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME).so
LINUX_AMD64_CC ?=
LINUX_AMD64_CC_BIN := $(firstword $(LINUX_AMD64_CC))

# --- dev helpers (mac host + docker CPA) ---
# ZIG: cross C compiler for linux/amd64 .so (brew install zig). Override if zig is elsewhere.
ZIG ?= zig
# CPA_PLUGINS_DIR: host path mounted into your docker CPA as its plugins dir.
# Defaults to the author's docker layout; override for your own.
CPA_PLUGINS_DIR ?= /Users/flame/CLIProxyAPI/plugins
# CPA host:port the vite dev server proxies API calls to.
CPA_HOST ?= http://127.0.0.1:8317

.PHONY: test vet web-build build-platform-go build-platform build-windows-amd64 build-linux-amd64 build-linux-amd64-go build package-platform package install-local install-linux-amd64 smoke-local dev-so dev-ui clean

test:
	$(GO) test ./...

# Build the single-file admin UI into web/dist/index.html (embedded by go:embed).
# Every plugin library build depends on this so the .so/.dll always carries a fresh UI.
web-build:
	cd web && npm install && VITE_HOSTED=1 npm run build

vet:
	$(GO) vet ./...

# Compile the c-shared library only (assumes web/dist/index.html is already current).
# Prefer build-platform / package-platform, which force web-build first.
build-platform-go:
	@if [ -z "$(GOOS)" ] || [ -z "$(GOARCH)" ]; then echo "GOOS and GOARCH are required"; exit 1; fi
	@case "$(GOOS)" in windows) ext=".dll" ;; darwin) ext=".dylib" ;; *) ext=".so" ;; esac; \
	out="$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)$$ext"; \
	mkdir -p "$$(dirname "$$out")"; \
	if [ -n "$(BUILD_CC)" ]; then export CC="$(BUILD_CC)"; fi; \
	CGO_ENABLED=1 GOOS="$(GOOS)" GOARCH="$(GOARCH)" $(GO) build -trimpath -buildmode=c-shared -ldflags='$(LDFLAGS) $(VERSION_LDFLAGS)' -o "$$out" .

# Public single-platform build: always rebuild the embedded admin UI first.
build-platform: web-build
	@$(MAKE) --no-print-directory build-platform-go \
		GOOS="$(GOOS)" GOARCH="$(GOARCH)" GO="$(GO)" DIST_DIR="$(DIST_DIR)" \
		PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(BUILD_CC)" \
		LDFLAGS="$(LDFLAGS)" VERSION_LDFLAGS="$(VERSION_LDFLAGS)"

build-windows-amd64:
	$(MAKE) --no-print-directory build-platform GOOS=windows GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)"

build-linux-amd64:
	@if [ -z "$(LINUX_AMD64_CC)" ]; then echo "LINUX_AMD64_CC is required for linux amd64 cgo cross-compile on Windows"; exit 1; fi
	@if ! command -v "$(LINUX_AMD64_CC_BIN)" >/dev/null 2>&1; then echo "Linux amd64 cross compiler not found: $(LINUX_AMD64_CC)"; exit 1; fi
	$(MAKE) --no-print-directory build-platform GOOS=linux GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(LINUX_AMD64_CC)"

# Multi-platform local build: web-build once, then compile each platform.
# linux amd64 still requires LINUX_AMD64_CC (same as before).
build: web-build
	@$(MAKE) --no-print-directory build-platform-go GOOS=windows GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)"
	@$(MAKE) --no-print-directory build-linux-amd64-go GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(LINUX_AMD64_CC)"

build-linux-amd64-go:
	@if [ -z "$(LINUX_AMD64_CC)" ]; then echo "LINUX_AMD64_CC is required for linux amd64 cgo cross-compile on Windows"; exit 1; fi
	@if ! command -v "$(LINUX_AMD64_CC_BIN)" >/dev/null 2>&1; then echo "Linux amd64 cross compiler not found: $(LINUX_AMD64_CC)"; exit 1; fi
	@$(MAKE) --no-print-directory build-platform-go GOOS=linux GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(LINUX_AMD64_CC)"

package-platform: build-platform
	@if [ -z "$(VERSION)" ]; then echo "VERSION is required"; exit 1; fi
	@case "$(GOOS)" in windows) ext=".dll" ;; darwin) ext=".dylib" ;; *) ext=".so" ;; esac; \
	library="$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)$$ext"; \
	archive="$(DIST_DIR)/$(PLUGIN_NAME)_$(VERSION)_$(GOOS)_$(GOARCH).zip"; \
	GOOS= GOARCH= CGO_ENABLED= $(GO) run .github/scripts/package-release.go -library "$$library" -archive "$$archive" -checksum "$$archive.sha256"

package:
	@if [ -n "$(GOOS)" ] || [ -n "$(GOARCH)" ]; then \
		$(MAKE) --no-print-directory package-platform VERSION="$(VERSION)" GOOS="$(GOOS)" GOARCH="$(GOARCH)" GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(BUILD_CC)" LDFLAGS="$(LDFLAGS)" VERSION_LDFLAGS="$(VERSION_LDFLAGS)"; \
	else \
		$(GO) run .github/scripts/package-release.go -version "$(VERSION)" -dist "$(DIST_DIR)" -out "$(DIST_DIR)/release"; \
	fi

install-local: build-windows-amd64
	@if [ -z "$(CPA_PLUGINS_DIR)" ]; then echo "CPA_PLUGINS_DIR is required"; exit 1; fi
	mkdir -p "$(CPA_PLUGINS_DIR)"
	cp $(WINDOWS_AMD64_OUT) "$(CPA_PLUGINS_DIR)/$(PLUGIN_NAME).dll"

install-linux-amd64: build-linux-amd64
	@if [ -z "$(CPA_PLUGINS_DIR)" ]; then echo "CPA_PLUGINS_DIR is required"; exit 1; fi
	mkdir -p "$(CPA_PLUGINS_DIR)"
	cp $(LINUX_AMD64_OUT) "$(CPA_PLUGINS_DIR)/$(PLUGIN_NAME).so"

smoke-local:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	$(GO) run .github/scripts/smoke-local.go

# dev-so: cross-compile linux/amd64 .so via zig (with fresh UI) and copy it
# into your docker CPA's mounted plugins dir, then the host hot-reloads it.
dev-so: web-build
	@$(MAKE) --no-print-directory build-platform-go GOOS=linux GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(ZIG) cc -target x86_64-linux-gnu" LDFLAGS="$(LDFLAGS)" VERSION_LDFLAGS=""
	@mkdir -p "$(CPA_PLUGINS_DIR)/linux/amd64"
	cp $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME).so "$(CPA_PLUGINS_DIR)/linux/amd64/$(PLUGIN_NAME).so"
	@echo ">> deployed $(PLUGIN_NAME).so -> $(CPA_PLUGINS_DIR)/linux/amd64/ (restart/reload your CPA to pick it up)"

# dev-ui: vite dev server on :5173, proxying /v0/management to your CPA.
dev-ui:
	cd web && CPA_DEV_CPA_HOST="$(CPA_HOST)" npm run dev

clean:
	rm -rf $(DIST_DIR)
