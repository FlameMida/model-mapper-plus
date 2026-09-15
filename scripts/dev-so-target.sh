#!/usr/bin/env bash
# 解析 make dev-so 的目标平台，打印 goos/goarch（例如 darwin/arm64、linux/amd64）。
# 优先级：显式 DEV_SO_GOOS/DEV_SO_GOARCH → 已有 CPA 容器 → 已部署插件目录 → 本机 go env / uname。
set -euo pipefail

DOCKER="${DOCKER:-docker}"

normalize_arch() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    x86_64|amd64|x64) echo amd64 ;;
    arm64|aarch64) echo arm64 ;;
    *) echo "" ;;
  esac
}

normalize_os() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    linux) echo linux ;;
    darwin|macos|osx) echo darwin ;;
    windows|win32|mingw*|msys*|cygwin*) echo windows ;;
    *) echo "" ;;
  esac
}

host_os() {
  if command -v go >/dev/null 2>&1; then
    os=$(go env GOOS 2>/dev/null || true)
    os=$(normalize_os "$os")
    if [ -n "$os" ]; then
      echo "$os"
      return
    fi
  fi
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    MINGW*|MSYS*|CYGWIN*) echo windows ;;
    *) echo "" ;;
  esac
}

host_arch() {
  if command -v go >/dev/null 2>&1; then
    arch=$(normalize_arch "$(go env GOARCH 2>/dev/null || true)")
    if [ -n "$arch" ]; then
      echo "$arch"
      return
    fi
  fi
  normalize_arch "$(uname -m)"
}

emit() {
  os=$(normalize_os "$1")
  arch=$(normalize_arch "$2")
  if [ -z "$os" ] || [ -z "$arch" ]; then
    echo "unsupported plugin target '$1/$2'" >&2
    exit 1
  fi
  echo "$os/$arch"
}

cpa_port() {
  local host="${CPA_HOST:-http://127.0.0.1:8317}"
  host="${host#*://}"
  host="${host%%/*}"
  if [ "${host##*:}" != "$host" ]; then
    echo "${host##*:}"
  else
    echo 8317
  fi
}

docker_inspect() {
  local ref=$1
  local os arch
  command -v "$DOCKER" >/dev/null 2>&1 || return 1
  os=$("$DOCKER" inspect -f '{{.Os}}' "$ref" 2>/dev/null || true)
  arch=$("$DOCKER" inspect -f '{{.Architecture}}' "$ref" 2>/dev/null || true)
  os=$(normalize_os "${os:-linux}")
  arch=$(normalize_arch "$arch")
  if [ -n "$os" ] && [ -n "$arch" ]; then
    echo "$os $arch"
    return 0
  fi
  return 1
}

detect_docker() {
  [ "${DEV_SO_SKIP_DOCKER:-}" = "1" ] && return 1
  command -v "$DOCKER" >/dev/null 2>&1 || return 1

  local names=()
  if [ -n "${CPA_CONTAINER:-}" ]; then
    names+=("$CPA_CONTAINER")
  fi
  names+=("cli-proxy-api")

  local name info
  for name in "${names[@]}"; do
    info=$(docker_inspect "$name" || true)
    if [ -n "$info" ]; then
      echo "$info"
      return 0
    fi
  done

  local id
  id=$("$DOCKER" ps -q --filter "publish=$(cpa_port)" 2>/dev/null | head -1 || true)
  if [ -n "$id" ]; then
    info=$(docker_inspect "$id" || true)
    if [ -n "$info" ]; then
      echo "$info"
      return 0
    fi
  fi
  return 1
}

detect_plugins_dir() {
  local root="${CPA_PLUGINS_DIR:-}"
  [ -n "$root" ] && [ -d "$root" ] || return 1

  local matches=()
  local os arch ext path
  for os in linux darwin windows; do
    case "$os" in
      windows) ext=".dll" ;;
      darwin) ext=".dylib" ;;
      *) ext=".so" ;;
    esac
    for arch in amd64 arm64; do
      path="$root/$os/$arch/model-mapper-plus$ext"
      if [ -f "$path" ]; then
        matches+=("$os $arch")
      fi
    done
  done
  if [ "${#matches[@]}" -eq 1 ]; then
    echo "${matches[0]}"
    return 0
  fi
  return 1
}

if [ -n "${DEV_SO_GOOS:-}" ] || [ -n "${DEV_SO_GOARCH:-}" ]; then
  emit "${DEV_SO_GOOS:-$(host_os)}" "${DEV_SO_GOARCH:-$(host_arch)}"
  exit 0
fi

if info=$(detect_docker); then
  emit $info
  exit 0
fi

if info=$(detect_plugins_dir); then
  emit $info
  exit 0
fi

emit "$(host_os)" "$(host_arch)"
