#!/usr/bin/env bash
# 验证 Makefile PLUGIN_VERSION 计算逻辑（覆盖 spec 的核心 Scenario）。
set -euo pipefail
cd "$(dirname "$0")/.."
fail() { echo "FAIL: $*" >&2; exit 1; }

# 临时 tag 清理（脚本任何方式退出都清理，避免残留污染后续 git describe）
TAG="v8.8.8-test$$"
trap 'git tag -d "$TAG" >/dev/null 2>&1 || true' EXIT

# Scenario: VERSION 环境变量优先（覆盖 tag 与 SHA）
[ "$(make print-version VERSION=9.9.9)" = "9.9.9" ] || fail "VERSION override"

# Scenario: VERSION 带 v 前缀被剥离
[ "$(make print-version VERSION=v1.2.4)" = "1.2.4" ] || fail "VERSION v-prefix strip"

# Scenario: dev 构建用 SHA（HEAD 无 tag）
V=$(make print-version)
case "$V" in 0.0.0-dev.*) ;; *) fail "dev SHA got '$V'";; esac

# Scenario: untracked 新文件标记 dirty
F="./.version-test-dirty-$$"
echo x > "$F"
V=$(make print-version)
case "$V" in *.dirty) ok=1;; *) ok=0;; esac
rm -f "$F"
[ "$ok" -eq 1 ] || fail "dirty suffix (untracked) got '$V'"

# Scenario: HEAD tag 去 v 前缀
git tag "$TAG" HEAD >/dev/null 2>&1 || fail "cannot create temp tag"
V=$(make print-version)
case "$V" in 8.8.8-test*) ;; *) fail "HEAD tag v-strip got '$V'";; esac
git tag -d "$TAG" >/dev/null 2>&1

# Scenario: VERSION 环境变量优先于 tag
git tag "$TAG" HEAD >/dev/null 2>&1
V=$(make print-version VERSION=6.6.6)
git tag -d "$TAG" >/dev/null 2>&1
[ "$V" = "6.6.6" ] || fail "VERSION>tag got '$V'"

trap - EXIT
echo "all version checks passed"
