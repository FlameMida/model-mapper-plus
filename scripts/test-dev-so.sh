#!/usr/bin/env bash
# 验证 dev-so：注入 dev SHA 版本 + 固定名部署 + 清理 release 残留。
set -euo pipefail
cd "$(dirname "$0")/.."
fail() { echo "FAIL: $*" >&2; exit 1; }

TARGET=$(make -s print-dev-so-arch DEV_SO_SKIP_DOCKER=1 CPA_PLUGINS_DIR="")
[ -n "$TARGET" ] || fail "print-dev-so-arch is empty"
GOOS_T=${TARGET%/*}
GOARCH_T=${TARGET#*/}
case "$GOOS_T" in
  windows) EXT=".dll" ;;
  darwin) EXT=".dylib" ;;
  *) EXT=".so" ;;
esac
# 从非 Linux 交叉编译 linux .so 才需要 zig；缺失用 skip 码 80。
if [ "$GOOS_T" = linux ] && [ "$(uname -s)" != Linux ]; then
  command -v zig >/dev/null 2>&1 || { echo "SKIP: zig not installed"; exit 80; }
fi

TMPDIR_CPA=$(mktemp -d)
LOG=$(mktemp)
trap 'rm -rf "$TMPDIR_CPA" "$LOG"' EXIT

# 预置旧 release 残留（验证部署前清理）
mkdir -p "$TMPDIR_CPA/$GOOS_T/$GOARCH_T"
touch "$TMPDIR_CPA/$GOOS_T/$GOARCH_T/model-mapper-plus-v1.2.3$EXT"

if ! make dev-so CPA_PLUGINS_DIR="$TMPDIR_CPA" DEV_SO_SKIP_DOCKER=1 > "$LOG" 2>&1; then
  tail -20 "$LOG"
  fail "make dev-so failed"
fi
tail -2 "$LOG"

# 部署名固定（覆写式）
[ -f "$TMPDIR_CPA/$GOOS_T/$GOARCH_T/model-mapper-plus$EXT" ] || fail "fixed deploy name missing"
# release 残留已清理（避免 CPA cleanup 冲突）
[ ! -e "$TMPDIR_CPA/$GOOS_T/$GOARCH_T/model-mapper-plus-v1.2.3$EXT" ] || fail "stale release not cleaned"
# 部署产物内嵌注入的 dev SHA 版本（0.0.0-dev.<hex>）
# 注：源码字面量 "0.0.0-dev.unbuilt" 永在产物里（Go rodata，-X 只改变量指向不改字面量），
# 故用 hex 后缀区分"已注入"（git short SHA 以 hex 开头）vs 默认 unbuilt（u 开头）。
# 用变量捕获 + grep -E（读全部输入），避免 pipefail 下 grep -q 早退致 strings SIGPIPE 误判。
injected=$(strings "$TMPDIR_CPA/$GOOS_T/$GOARCH_T/model-mapper-plus$EXT" | grep -E '0\.0\.0-dev\.[0-9a-f]' || true)
[ -n "$injected" ] || fail "dev-so did not inject SHA version"

echo "dev-so checks passed"
