#!/usr/bin/env bash
# 验证 dev-so：注入 dev SHA 版本 + 固定名部署 + 清理 release 残留。
set -euo pipefail
cd "$(dirname "$0")/.."
# zig 缺失用 skip 码 80（区别于真正通过 0 / 失败 1），CI 可据此区分。
command -v zig >/dev/null 2>&1 || { echo "SKIP: zig not installed"; exit 80; }
fail() { echo "FAIL: $*" >&2; exit 1; }

TMPDIR_CPA=$(mktemp -d)
LOG=$(mktemp)
trap 'rm -rf "$TMPDIR_CPA" "$LOG"' EXIT

# 预置旧 release 残留（验证部署前清理）
mkdir -p "$TMPDIR_CPA/linux/amd64"
touch "$TMPDIR_CPA/linux/amd64/model-mapper-plus-v1.2.3.so"

if ! make dev-so CPA_PLUGINS_DIR="$TMPDIR_CPA" > "$LOG" 2>&1; then
  tail -20 "$LOG"
  fail "make dev-so failed"
fi
tail -2 "$LOG"

# 部署名固定（覆写式）
[ -f "$TMPDIR_CPA/linux/amd64/model-mapper-plus.so" ] || fail "fixed deploy name missing"
# release 残留已清理（避免 CPA cleanup 冲突）
[ ! -e "$TMPDIR_CPA/linux/amd64/model-mapper-plus-v1.2.3.so" ] || fail "stale release not cleaned"
# 部署的固定名 .so 内嵌注入的 dev SHA 版本（0.0.0-dev.<hex>）
# 注：源码字面量 "0.0.0-dev.unbuilt" 永在 .so（Go rodata，-X 只改变量指向不改字面量），
# 故用 hex 后缀区分"已注入"（git short SHA 以 hex 开头）vs 默认 unbuilt（u 开头）
strings "$TMPDIR_CPA/linux/amd64/model-mapper-plus.so" | grep -qE '0\.0\.0-dev\.[0-9a-f]' || fail "dev-so did not inject SHA version"

echo "dev-so checks passed"
