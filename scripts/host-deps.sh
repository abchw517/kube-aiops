#!/usr/bin/env bash
# Sourced by the deployment entry points so the selected tools stay on PATH.

host_deps_error() { printf '[host-deps][ERROR] %s\n' "$*" >&2; return 1; }
host_deps_log() { printf '[host-deps] %s\n' "$*" >&2; }

host_deps_go_ready() {
  local minimum="$1" version goroot tools
  local -a actual required
  version=$(GOTOOLCHAIN=local "$2" version 2>/dev/null) || return 1
  [[ "$version" =~ ^go\ version\ go([0-9]+)\.([0-9]+)\.([0-9]+)\  ]] || return 1
  actual=("${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}")
  IFS=. read -r -a required <<<"$minimum"
  (( actual[0] > required[0] || (actual[0] == required[0] &&
    (actual[1] > required[1] || (actual[1] == required[1] && actual[2] >= required[2]))) )) || return 1
  goroot=$(GOTOOLCHAIN=local "$2" env GOROOT 2>/dev/null) || return 1
  tools=$(GOTOOLCHAIN=local "$2" env GOTOOLDIR 2>/dev/null) || return 1
  [[ -d "$goroot/src/runtime" && -x "$tools/compile" ]]
}

host_deps_node_ready() {
  local version
  local PATH="${1:+$1:}$PATH"
  if [[ -n "${1:-}" ]]; then
    [[ -x "$1/node" && -x "$1/npm" && -x "$1/npx" ]] || return 1
  fi
  version=$(node --version 2>/dev/null) || return 1
  [[ "$version" =~ ^v([0-9]+)\.[0-9]+\.[0-9]+$ ]] && (( BASH_REMATCH[1] >= 22 )) &&
    npm --version >/dev/null 2>&1 && npx --version >/dev/null 2>&1
}

host_deps_download() {
  curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
    --retry 2 --connect-timeout 15 --max-time 300 --output "$2" "$1"
}

host_deps_install() (
  local tool="$1" target="$2" version="$3" arch machine checksum archive url checksum_url work
  [[ "$(uname -s)" == Linux ]] || { host_deps_error "自动安装 $tool 仅支持 Linux，请手动安装"; return 1; }
  machine=$(uname -m)
  case "$machine" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) host_deps_error "自动安装 $tool 不支持架构 $machine，请手动安装"; return 1 ;;
  esac

  case "$tool" in
    go)
      url="https://go.dev/dl/go${version}.linux-${arch}.tar.gz"
      case "$arch" in
        amd64) checksum=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053 ;;
        arm64) checksum=fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49 ;;
      esac ;;
    node)
      [[ "$arch" != amd64 ]] || arch=x64
      url="https://nodejs.org/dist/v${version}/node-v${version}-linux-${arch}.tar.gz"
      case "$arch" in
        x64) checksum=d36e56998220085782c0ca965f9d51b7726335aed2f5fc7321c6c0ad233aa96d ;;
        arm64) checksum=d32817b937219b8f131a28546035183d79e7fd17a86e38ccb8772901a7cd9009 ;;
      esac ;;
    kubectl)
      url="https://dl.k8s.io/release/${version}/bin/linux/${arch}/kubectl"
      checksum_url="${url}.sha256" ;;
    helm)
      url="https://get.helm.sh/helm-${version}-linux-${arch}.tar.gz"
      checksum_url="${url}.sha256sum" ;;
  esac

  local cmd
  for cmd in curl sha256sum tar; do
    command -v "$cmd" >/dev/null || { host_deps_error "自动安装 $tool 需要 $cmd，请先通过系统包管理器安装"; return 1; }
  done
  mkdir -p -- "${target%/*}" || return 1
  work=$(mktemp -d "${target%/*}/.${tool}.XXXXXX") || return 1
  trap 'rm -rf -- "$work"' EXIT
  archive="$work/${url##*/}"
  host_deps_log "下载 $tool $version ($machine)"
  host_deps_download "$url" "$archive" || { host_deps_error "下载失败：$url"; return 1; }
  if [[ -n "${checksum_url:-}" ]]; then
    host_deps_download "$checksum_url" "$work/checksum" || { host_deps_error "下载校验值失败：$checksum_url"; return 1; }
    read -r checksum _ <"$work/checksum" || [[ -n "$checksum" ]] || return 1
  fi
  if [[ ! "${checksum:-}" =~ ^[[:xdigit:]]{64}$ ]] ||
    ! printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check --status; then
    host_deps_error "$tool $version SHA-256 校验失败"
    return 1
  fi

  mkdir -- "$work/payload" || return 1
  case "$tool" in
    go|node)
      tar -xzf "$archive" --no-same-owner -C "$work/payload" --strip-components=1 || return 1
      if [[ "$tool" == go ]]; then
        unset GOROOT
        host_deps_go_ready "$version" "$work/payload/bin/go" || { host_deps_error "解压后的 Go 工具链不可用"; return 1; }
      else
        host_deps_node_ready "$work/payload/bin" || { host_deps_error "解压后的 Node.js、npm 或 npx 不可用"; return 1; }
      fi
      mv -nT -- "$work/payload" "$target" || return 1 ;;
    kubectl)
      chmod 0755 "$archive" && "$archive" version --client >/dev/null &&
        mv -nT -- "$archive" "$target" || return 1 ;;
    helm)
      tar -xzf "$archive" --no-same-owner -C "$work/payload" "linux-${arch}/helm" || return 1
      "$work/payload/linux-${arch}/helm" version --short >/dev/null &&
        mv -nT -- "$work/payload/linux-${arch}/helm" "$target" || return 1 ;;
  esac
)

host_deps_select_helm3() {
  local root="$1" version=v3.21.3 candidate reported managed
  local -a path_candidates
  managed="$root/helm-${version}/bin/helm"
  mapfile -t path_candidates < <(type -aP helm)
  for candidate in "${path_candidates[@]}" "$root/bin/helm" "$managed"; do
    [[ -x "$candidate" ]] || continue
    reported=$("$candidate" version --short 2>/dev/null) || continue
    if [[ "$reported" =~ ^v3\.[0-9]+\.[0-9]+([+.-]|$) ]]; then
      HOST_DEPS_HELM="$candidate"
      host_deps_log "Helm $HOST_DEPS_HELM ($reported)"
      return 0
    fi
  done

  if [[ -e "$managed" || -L "$managed" ]]; then
    host_deps_error "项目的 Helm 3 缓存不可用：$managed，请移走该文件后重试"
    return 1
  fi
  host_deps_log "未找到可用的 Helm 3，准备安装项目专用版本"
  host_deps_install helm "$managed" "$version" || return 1
  reported=$("$managed" version --short) || return 1
  if [[ ! "$reported" =~ ^v3\.[0-9]+\.[0-9]+([+.-]|$) ]]; then
    host_deps_error "安装后的 Helm 版本不兼容：$reported"
    return 1
  fi
  HOST_DEPS_HELM="$managed"
  host_deps_log "Helm $HOST_DEPS_HELM ($reported)"
}

host_deps_ensure() {
  local profile="$1" root repo cmd minimum go_dir node_dir cluster_bin
  local GO_VERSION KUBERNETES_VERSION
  local node_version=22.19.0
  root="${KUBE_AIOPS_TOOLS_DIR:-${HOME:?}/.local/share/kube-aiops/tools}"
  [[ "$root" == /* ]] || root="$PWD/$root"
  repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd) || return 1
  # shellcheck source=config/platform-versions.env
  source "$repo/config/platform-versions.env" || return 1
  case "$profile" in
    portal)
      for cmd in openssl curl awk; do
        command -v "$cmd" >/dev/null || { host_deps_error "缺少 $cmd，请先通过系统包管理器安装"; return 1; }
      done
      minimum=$(awk '$1 == "go" { print $2; exit }' "$repo/go.mod") || return 1
      [[ "$minimum" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { host_deps_error "无法读取 go.mod 中的最低 Go 版本"; return 1; }
      if ! host_deps_go_ready "$minimum" go; then
        go_dir="$root/go${GO_VERSION}"
        [[ -e "$go_dir" ]] || host_deps_install go "$go_dir" "$GO_VERSION" || return 1
        if ! (unset GOROOT; host_deps_go_ready "$minimum" "$go_dir/bin/go"); then
          host_deps_error "Go 安装目录不可用：$go_dir，请移走该目录后重试"
          return 1
        fi
        unset GOROOT
        export PATH="$go_dir/bin:$PATH"
      fi
      if ! host_deps_node_ready; then
        node_dir="$root/node-v${node_version}"
        [[ -e "$node_dir" ]] || host_deps_install node "$node_dir" "$node_version" || return 1
        if ! host_deps_node_ready "$node_dir/bin"; then
          host_deps_error "Node.js 安装目录不可用：$node_dir，请移走该目录后重试"
          return 1
        fi
        export PATH="$node_dir/bin:$PATH"
      fi
      host_deps_log "Go $(command -v go)；Node.js $(command -v node)" ;;
    cluster|kubectl)
      cluster_bin="$root/bin"
      export PATH="$PATH:$cluster_bin"
      if ! command -v kubectl >/dev/null; then
        host_deps_install kubectl "$cluster_bin/kubectl" "$KUBERNETES_VERSION" || return 1
        command -v kubectl >/dev/null || { host_deps_error "kubectl 安装目录不可用：$cluster_bin"; return 1; }
      fi
      if [[ "$profile" == cluster ]]; then
        host_deps_log "kubectl $(command -v kubectl)"
        host_deps_select_helm3 "$root" || return 1
      fi ;;
    helm) host_deps_select_helm3 "$root" ;;
    *) host_deps_error '用法：bash scripts/host-deps.sh {portal|cluster|helm}'; return 1 ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  host_deps_ensure "${1:-}"
fi
