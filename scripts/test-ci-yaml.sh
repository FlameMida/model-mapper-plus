#!/usr/bin/env bash
# 断言 build.yml 已注入 dev SHA 计算与版本化 output/library 路径
# （CI 难本地全跑，用结构断言 grep -F 字面匹配）。
set -euo pipefail
cd "$(dirname "$0")/.."
f=.github/workflows/build.yml
fail() { echo "FAIL: $*" >&2; exit 1; }

grep -qF 'VERSION="0.0.0-dev.$(git rev-parse --short HEAD)"' "$f" || fail "non-tag SHA missing"
grep -qF 'output: ${{ env.PLUGIN_NAME }}-v${{ env.VERSION }}.dll' "$f" || fail "windows output not versioned"
grep -qF 'output: ${{ env.PLUGIN_NAME }}-v${{ env.VERSION }}.so' "$f" || fail "freebsd output not versioned"
grep -qF -- '-library "dist/windows_arm64/${PLUGIN_NAME}-v${VERSION}.dll"' "$f" || fail "windows -library path not versioned"
grep -qF -- '-library "dist/freebsd_amd64/${PLUGIN_NAME}-v${VERSION}.so"' "$f" || fail "freebsd -library path not versioned"

echo "ci yaml checks passed"
