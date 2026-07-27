PLUGIN_NAME := model-mapper-plus
DIST_DIR := dist
GO ?= go
GOOS ?=
GOARCH ?=
BUILD_CC ?=
VERSION ?=
LDFLAGS ?= -s -w
# 版本号单一来源：VERSION 强制覆盖 > HEAD 精确 tag > dev SHA；VERSION 与 tag 均去 v 前缀
# （CPA 拒绝以 v 开头的 Version）。始终非空（CPA validPlugin 强制）。
# 注意：PLUGIN_VERSION 在顶层求值一次，递归 make 须显式传入（命令行覆盖子 make 重算），
# 否则 web-build 改 tracked 文件致工作区 dirty 后子 make 重算会与父值不一致。
HEAD_TAG  := $(shell git describe --tags --exact-match HEAD 2>/dev/null | head -1)
GIT_SHORT := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_DIRTY := $(shell [ -z "$$(git status --porcelain 2>/dev/null)" ] || echo ".dirty")
ifneq ($(VERSION),)
  PLUGIN_VERSION := $(patsubst v%,%,$(VERSION))
else ifneq ($(HEAD_TAG),)
  PLUGIN_VERSION := $(patsubst v%,%,$(HEAD_TAG))
else ifneq ($(GIT_SHORT),)
  PLUGIN_VERSION := 0.0.0-dev.$(GIT_SHORT)$(GIT_DIRTY)
else
  PLUGIN_VERSION := 0.0.0-dev.unknown
endif
VERSION_LDFLAGS := -X main.pluginVersion=$(PLUGIN_VERSION)
WINDOWS_AMD64_OUT := $(DIST_DIR)/windows_amd64/$(PLUGIN_NAME)-v$(PLUGIN_VERSION).dll
LINUX_AMD64_OUT := $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME)-v$(PLUGIN_VERSION).so
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

.PHONY: test test-scripts vet web-build build-platform-go build-platform build-windows-amd64 build-linux-amd64 build-linux-amd64-go build package-platform package install-local install-linux-amd64 smoke-local smoke-persistence dev-so dev-ui clean print-version

test:
	$(GO) test ./...

# 版本计算与 CI yaml 的结构断言（dev-so 需 zig，单独跑，不在此自动执行）。
test-scripts:
	@bash scripts/test-version.sh
	@bash scripts/test-ci-yaml.sh

# 打印当前 PLUGIN_VERSION（调试与脚本用）。
print-version:
	@echo "$(PLUGIN_VERSION)"

# Build the single-file admin UI into web/dist/index.html (embedded by go:embed).
# Every plugin library build depends on this so the .so/.dll always carries a fresh UI.
web-build:
	cd web && npm install && VITE_HOSTED=1 npm run build

vet:
	$(GO) vet ./...

# Compile the c-shared library only (assumes web/dist/index.html is already current).
# Prefer build-platform / package-platform, which force web-build first.
#
# Go 1.26 writes the output basename into the LIBRARY directive of the
# export_file.def it generates for -buildmode=c-shared on Windows, and GNU ld
# rejects the extra dots a version string adds ("export_file.def:1: syntax
# error"; golang/go#78238). Build under the plain plugin name — the same shape
# key-policy ships — then rename to the versioned artifact name. The DLL's
# internal name is irrelevant to CPA, which loads plugins by path.
build-platform-go:
	@if [ -z "$(GOOS)" ] || [ -z "$(GOARCH)" ]; then echo "GOOS and GOARCH are required"; exit 1; fi
	@case "$(GOOS)" in windows) ext=".dll" ;; darwin) ext=".dylib" ;; *) ext=".so" ;; esac; \
	dir="$(DIST_DIR)/$(GOOS)_$(GOARCH)"; \
	out="$$dir/$(PLUGIN_NAME)-v$(PLUGIN_VERSION)$$ext"; \
	staged="$$dir/$(PLUGIN_NAME)$$ext"; \
	mkdir -p "$$dir"; \
	if [ -n "$(BUILD_CC)" ]; then export CC="$(BUILD_CC)"; fi; \
	CGO_ENABLED=1 GOOS="$(GOOS)" GOARCH="$(GOARCH)" $(GO) build -trimpath -buildmode=c-shared -ldflags='$(LDFLAGS) $(VERSION_LDFLAGS)' -o "$$staged" .; \
	rm -f "$$dir/$(PLUGIN_NAME).h"; \
	mv -f "$$staged" "$$out"

# Public single-platform build: always rebuild the embedded admin UI first.
build-platform: web-build
	@$(MAKE) --no-print-directory build-platform-go \
		GOOS="$(GOOS)" GOARCH="$(GOARCH)" GO="$(GO)" DIST_DIR="$(DIST_DIR)" \
		PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(BUILD_CC)" PLUGIN_VERSION="$(PLUGIN_VERSION)" \
		LDFLAGS="$(LDFLAGS)" VERSION_LDFLAGS="$(VERSION_LDFLAGS)"

build-windows-amd64:
	$(MAKE) --no-print-directory build-platform GOOS=windows GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" PLUGIN_VERSION="$(PLUGIN_VERSION)"

build-linux-amd64:
	@if [ -z "$(LINUX_AMD64_CC)" ]; then echo "LINUX_AMD64_CC is required for linux amd64 cgo cross-compile on Windows"; exit 1; fi
	@if ! command -v "$(LINUX_AMD64_CC_BIN)" >/dev/null 2>&1; then echo "Linux amd64 cross compiler not found: $(LINUX_AMD64_CC)"; exit 1; fi
	$(MAKE) --no-print-directory build-platform GOOS=linux GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" PLUGIN_VERSION="$(PLUGIN_VERSION)" BUILD_CC="$(LINUX_AMD64_CC)"

# Multi-platform local build: web-build once, then compile each platform.
# linux amd64 still requires LINUX_AMD64_CC (same as before).
build: web-build
	@$(MAKE) --no-print-directory build-platform-go GOOS=windows GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" PLUGIN_VERSION="$(PLUGIN_VERSION)"
	@$(MAKE) --no-print-directory build-linux-amd64-go GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" PLUGIN_VERSION="$(PLUGIN_VERSION)" BUILD_CC="$(LINUX_AMD64_CC)"

build-linux-amd64-go:
	@if [ -z "$(LINUX_AMD64_CC)" ]; then echo "LINUX_AMD64_CC is required for linux amd64 cgo cross-compile on Windows"; exit 1; fi
	@if ! command -v "$(LINUX_AMD64_CC_BIN)" >/dev/null 2>&1; then echo "Linux amd64 cross compiler not found: $(LINUX_AMD64_CC)"; exit 1; fi
	@$(MAKE) --no-print-directory build-platform-go GOOS=linux GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" PLUGIN_VERSION="$(PLUGIN_VERSION)" BUILD_CC="$(LINUX_AMD64_CC)"

package-platform: build-platform
	@if [ -z "$(VERSION)" ]; then echo "VERSION is required"; exit 1; fi
	@case "$(GOOS)" in windows) ext=".dll" ;; darwin) ext=".dylib" ;; *) ext=".so" ;; esac; \
	library="$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)-v$(PLUGIN_VERSION)$$ext"; \
	archive="$(DIST_DIR)/$(PLUGIN_NAME)_$(VERSION)_$(GOOS)_$(GOARCH).zip"; \
	GOOS= GOARCH= CGO_ENABLED= $(GO) run .github/scripts/package-release.go -library "$$library" -archive "$$archive" -checksum "$$archive.sha256"

package:
	@if [ -n "$(GOOS)" ] || [ -n "$(GOARCH)" ]; then \
		$(MAKE) --no-print-directory package-platform VERSION="$(VERSION)" GOOS="$(GOOS)" GOARCH="$(GOARCH)" GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" PLUGIN_VERSION="$(PLUGIN_VERSION)" BUILD_CC="$(BUILD_CC)" LDFLAGS="$(LDFLAGS)" VERSION_LDFLAGS="$(VERSION_LDFLAGS)"; \
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

# smoke-persistence: rules saved to state_file must survive a CPA restart.
# Requires CPA_SMOKE_RESTART_CMD in .env.
smoke-persistence:
	@if [ -f .env ]; then set -a; . ./.env; set +a; fi; \
	$(GO) run .github/scripts/smoke-persistence.go

# dev-so: cross-compile linux/amd64 .so via zig (with fresh UI + dev SHA version)
# and copy it as the FIXED name model-mapper-plus.so into your docker CPA's plugins
# dir (cp 覆盖，热重载友好）。部署前清理该 ID 的 release 残留（避免 CPA cleanup 冲突）。
dev-so: web-build
	@$(MAKE) --no-print-directory build-platform-go GOOS=linux GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" PLUGIN_VERSION="$(PLUGIN_VERSION)" BUILD_CC="$(ZIG) cc -target x86_64-linux-gnu" LDFLAGS="$(LDFLAGS)" VERSION_LDFLAGS="$(VERSION_LDFLAGS)"
	@mkdir -p "$(CPA_PLUGINS_DIR)/linux/amd64"
	@rm -f "$(CPA_PLUGINS_DIR)/linux/amd64/$(PLUGIN_NAME)-v"*.so
	cp $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME)-v$(PLUGIN_VERSION).so "$(CPA_PLUGINS_DIR)/linux/amd64/$(PLUGIN_NAME).so"
	@echo ">> deployed $(PLUGIN_NAME).so (v$(PLUGIN_VERSION)) -> $(CPA_PLUGINS_DIR)/linux/amd64/ (restart/reload your CPA to pick it up)"

# dev-ui: vite dev server on :5173, proxying /v0/management to your CPA.
dev-ui:
	cd web && CPA_DEV_CPA_HOST="$(CPA_HOST)" npm run dev

clean:
	rm -rf $(DIST_DIR)
