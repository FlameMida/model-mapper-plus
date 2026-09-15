#!/usr/bin/env bash
# 验证 make dev-so 目标平台自动识别（显式覆盖 / 容器 / 已部署目录 / 本机）。
set -euo pipefail
cd "$(dirname "$0")/.."
fail() { echo "FAIL: $*" >&2; exit 1; }

resolve() {
  bash scripts/dev-so-target.sh
}

case "$(uname -m)" in
  x86_64|amd64) host_arch=amd64 ;;
  arm64|aarch64) host_arch=arm64 ;;
  *) fail "unsupported host arch: $(uname -m)" ;;
esac
case "$(uname -s)" in
  Linux) host_os=linux ;;
  Darwin) host_os=darwin ;;
  MINGW*|MSYS*|CYGWIN*) host_os=windows ;;
  *) fail "unsupported host os: $(uname -s)" ;;
esac

got=$(DEV_SO_SKIP_DOCKER=1 CPA_PLUGINS_DIR="" resolve)
[ "$got" = "$host_os/$host_arch" ] || fail "host fallback: got '$got' want '$host_os/$host_arch'"

got=$(DEV_SO_GOOS=linux DEV_SO_GOARCH=amd64 resolve)
[ "$got" = "linux/amd64" ] || fail "explicit linux/amd64: got '$got'"

got=$(DEV_SO_GOOS=darwin DEV_SO_GOARCH=arm64 resolve)
[ "$got" = "darwin/arm64" ] || fail "explicit darwin/arm64: got '$got'"

got=$(DEV_SO_GOARCH=amd64 DEV_SO_SKIP_DOCKER=1 CPA_PLUGINS_DIR="" resolve)
[ "$got" = "$host_os/amd64" ] || fail "explicit arch only: got '$got'"

fake=$(mktemp)
cat > "$fake" <<'EOF'
#!/bin/sh
case "$1 $2 $3" in
  "inspect -f {{.Os}}") echo linux; exit 0 ;;
  "inspect -f {{.Architecture}}") echo amd64; exit 0 ;;
esac
exit 1
EOF
chmod +x "$fake"
got=$(DOCKER="$fake" CPA_CONTAINER=cli-proxy-api CPA_PLUGINS_DIR="" resolve)
[ "$got" = "linux/amd64" ] || fail "docker inspect: got '$got'"

plug=$(mktemp -d)
mkdir -p "$plug/linux/amd64"
touch "$plug/linux/amd64/model-mapper-plus.so"
got=$(DEV_SO_SKIP_DOCKER=1 CPA_PLUGINS_DIR="$plug" resolve)
[ "$got" = "linux/amd64" ] || fail "plugins dir: got '$got'"
rm -rf "$plug" "$fake"

got=$(make -s print-dev-so-arch DEV_SO_GOOS=linux DEV_SO_GOARCH=arm64)
[ "$got" = "linux/arm64" ] || fail "make print-dev-so-arch: got '$got'"

echo "dev-so arch checks passed"
