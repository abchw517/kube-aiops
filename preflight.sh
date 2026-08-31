#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${ROOT_DIR}"

log() { printf '[preflight] %s\n' "$*"; }
fail() { printf '[preflight][ERROR] %s\n' "$*" >&2; exit 1; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "缺少依赖命令: $1"
}

require_cmd bash
require_cmd find
require_cmd grep
require_cmd python3

log "1/8 Shell 语法检查"
while IFS= read -r -d '' file; do
  bash -n "$file"
done < <(find . -type f -name '*.sh' -not -path './.git/*' -print0)

if command -v shellcheck >/dev/null 2>&1; then
  log "2/8 ShellCheck"
  mapfile -d '' shell_files < <(find . -type f -name '*.sh' -not -path './.git/*' -print0)
  if ((${#shell_files[@]} > 0)); then
    shellcheck -x "${shell_files[@]}"
  fi
else
  log "2/8 ShellCheck: 本地未安装，跳过（CI 中强制执行）"
fi

log "3/8 YAML 语法检查"
python3 - <<'PY'
import pathlib
import sys

try:
    import yaml
except Exception:
    print('[preflight][ERROR] 缺少 Python PyYAML：pip install pyyaml', file=sys.stderr)
    sys.exit(1)

files = [p for p in pathlib.Path('.').rglob('*') if p.is_file() and p.suffix in {'.yaml', '.yml'} and '.git' not in p.parts]
for path in files:
    try:
        with path.open('r', encoding='utf-8') as f:
            list(yaml.safe_load_all(f))
    except Exception as exc:
        print(f'[preflight][ERROR] YAML 解析失败: {path}: {exc}', file=sys.stderr)
        sys.exit(1)
print(f'[preflight] YAML 文件解析通过: {len(files)}')
PY

if command -v kubeconform >/dev/null 2>&1; then
  log "4/8 Kubernetes Schema 检查（kubeconform）"

  # 只校验真正提交给 Kubernetes API Server 的 Manifest。
  # Helm values 文件不是 Kubernetes 对象，不包含 apiVersion/kind，不能交给 kubeconform。
  mapfile -d '' manifest_files < <(
    find deploy \
      -type f \
      \( -name '*.yaml' -o -name '*.yml' \) \
      ! -name 'values.yaml' \
      ! -name 'values-*.yaml' \
      -print0
  )

  if ((${#manifest_files[@]} == 0)); then
    fail "未找到可供 kubeconform 校验的 Kubernetes Manifest"
  fi

  kubeconform \
    -strict \
    -summary \
    -ignore-missing-schemas \
    -kubernetes-version 1.34.0 \
    "${manifest_files[@]}"
else
  log "4/8 kubeconform: 本地未安装，跳过（CI 中强制执行）"
fi

log "5/8 静态 Secret 泄露检查"
# 只检查高置信度模式；完整历史扫描由 CI 的 Gitleaks 执行。
if grep -RInE \
  --exclude-dir=.git \
  --exclude='README.md' \
  --exclude='preflight.sh' \
  '(-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----|sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,}|gh[pousr]_[A-Za-z0-9]{20,})' .; then
  fail "检测到疑似真实凭据，请移除后再提交"
fi

log "6/8 RBAC 静态安全检查"
python3 scripts/rbac_lint.py

log "7/8 Python 语法检查"
python3 -m py_compile scripts/*.py

log "8/8 安全控制回归测试"
python3 tests/security-controls-test.py
bash tests/make-security-test.sh

log "全部检查通过"
