# kube-aiops 安装与部署

在已有 Kubernetes 集群中按顺序完成 [预检](#1-依赖和预检) → [安装 K8sGPT](#2-安装-k8sgpt) → [准备镜像](#3-准备-api-和-web-镜像) → [部署 API 与 Web Portal](#4-部署-api-与-web-portal)。已有 K8sGPT 部署时，先核对其 Namespace、实例名和 Result。API 清单默认读取 `k8sgpt-operator-system/k8sgpt-engine`，使用其他实例时，按实际配置调整 API 清单中的 `K8SGPT_NAMESPACE` 和 `K8SGPT_NAME`，并核对 ServiceAccount 的读取权限。

开发机工具链和本地回归见 [开发指南](development-standard.md#本地开发与验证)。已有 Portal 的发布直接走 [存量部署升级](#存量部署升级)。

## 从旧目录布局升级

升级前检查 CI、镜像构建脚本和文档链接中硬编码的路径。

| 用途 | 旧路径 | 当前路径 |
| --- | --- | --- |
| 安装部署指南 | `docs/install-deployment.md` | `docx/install-deployment.md` |
| Go API 入口 | `./cmd/api` | `./api/cmd/api` |
| 生成的 Web API 客户端 | `clients/typescript/generated.ts` | `web/src/api/generated.ts` |
| Web NGINX 配置 | `web/nginx.conf` | `config/web/nginx.conf` |

从仓库根目录运行 `make api-build`、`make web-build` 和 `make api-contract-check` 验证构建与接口契约。生产镜像按[准备镜像](#3-准备-api-和-web-镜像)构建，API 和 Web 均使用仓库根目录作为 Docker 构建上下文。

正式发布分别使用 `deploy/api/` 和 `deploy/web/`。可观测性示例中的 Deployment 与正式清单使用相同的资源名，使用示例时从 `deploy/examples/observability/` 选择所需文件并按[可观测性配置](#配置可观测性来源)渲染，再应用生成的清单。

## 0. 验证 Portal Demo

在仓库根目录启动 Demo。尚未获取源码时，先执行第 1 节的克隆命令。Demo 使用简体中文样例数据，可在没有 kubeconfig、Kubernetes 集群和监控凭据的环境中运行。

```bash
PORTAL_DEMO_HOST=0.0.0.0 PORTAL_DEMO_PORT=4173 make portal-demo
```

从可访问部署主机的浏览器打开 `http://<服务器IP>:4173/#/findings`，查看样例诊断和关联证据。仅本机查看时执行 `make portal-demo`，打开 <http://127.0.0.1:4173/#/findings>。自动检查 Demo 和四类证据来源时，先停止正在运行的 Demo，再执行：

```bash
make portal-demo-check
```

检查结束后脚本自动退出并清理本次运行的临时文件。手动启动的 Demo 在启动终端按 Ctrl+C 停止，脚本会结束 Demo 进程并清理本次临时文件。[Portal Demo](portal-demo.md)

## 1. 依赖和预检

安装端准备 Git、Make、Bash、Python 3、PyYAML、curl、tar、sha256sum 和可访问目标集群的 kubeconfig。`make cluster-deps` 检查并安装缺少的 kubectl 和 Helm 3。构建镜像需要 Docker Buildx 和仓库推送权限。读取构建元数据和命令行验收需要 jq。节点需能拉取所用镜像，Engine Pod 需能访问 AI Provider。版本基线见 [platform-versions.env](../config/platform-versions.env)。

首次获取源码时执行：

```bash
git clone https://github.com/abchw517/kube-aiops.git
cd kube-aiops
```

在仓库根目录使用同一个 Bash 会话执行部署命令。默认以 kubeconfig 的当前 context 为目标，执行安装前确认输出的集群名称。

```bash
set -euo pipefail
make cluster-deps
export PATH="$PATH:${KUBE_AIOPS_TOOLS_DIR:-$HOME/.local/share/kube-aiops/tools}/bin"

target_context="$(kubectl config current-context)"
```

如需切换集群，单独赋值并切换 context。

```bash
target_context=your-cluster-context
kubectl config use-context "$target_context"
```

核对目标 context 和依赖。

```bash
printf '目标 context：%s\n' "$target_context"
kubectl version
python3 -c 'import yaml'
make platform-check
```

安装和验收使用 Helm 3，工具缓存位置可通过 `KUBE_AIOPS_TOOLS_DIR` 指定。安装身份需要管理项目 CRD、Namespace、Secret、Lease、RBAC 和工作负载，并可模拟 ServiceAccount 进行授权检查。

早期部署若需迁移 Legacy RBAC 所有权，首次执行 `MIGRATE_LEGACY_OWNERSHIP=true make install`。安装器验证角色规则、绑定主体和所有权后才迁移。

## 2. 安装 K8sGPT

使用内网仓库时，先按 [使用内网镜像仓库](#使用内网镜像仓库) 同步 Operator、RBAC Proxy 和 Engine。默认 CR 使用 OpenAI、`gpt-4o-mini` 和 5 分钟分析周期。需要自定义模型、语言或兼容端点时，执行 [配置 Engine 模型与语言](#配置-engine-模型与语言) 中的安装步骤，然后继续 [验证部署](#验证部署)。

```bash
make bootstrap

provider_secret="$(kubectl -n k8sgpt-operator-system get secret k8sgpt-openai-secret --ignore-not-found -o name)"
if [[ -z "$provider_secret" ]]; then
  read -rsp 'Provider API key: ' OPENAI_TOKEN
  printf '\n'
  (export OPENAI_TOKEN; make bootstrap-secret)
  unset OPENAI_TOKEN
fi

K8SGPT_OPERATOR_MEMORY_LIMIT=1Gi make install
make verify
make status
make results
```

`1Gi` 是 Operator 内存上限示例。已有部署时，按本次发布参数运行 `make install` 进行升级。使用内网仓库时，按下方对应小节调整安装命令。切换 Provider 时，先用对应 API Key 更新 Secret。

当前 Engine 为 `v0.4.41`。升级时保留现有模型、Base URL 和资源覆盖参数。需要中文诊断时，设置 `K8SGPT_AI_LANGUAGE=Chinese`。使用内网仓库时，先同步新版 Engine 镜像，再执行安装或升级。

### 使用内网镜像仓库

将下面三张镜像同步到节点可访问的镜像仓库，并保留各自的版本标签。`K8SGPT_IMAGE_REGISTRY` 指定 `仓库地址[:端口][/项目路径]`，安装器以 `仓库/镜像:版本` 拉取。

| 镜像 | 默认仓库 | 使用 `mharbor-cs.cloud.kemai.cn/public` 时 | 单独覆盖的变量 |
| --- | --- | --- | --- |
| Operator | `ghcr.io/k8sgpt-ai/k8sgpt-operator` | `mharbor-cs.cloud.kemai.cn/public/k8sgpt-operator:v0.2.29` | `K8SGPT_OPERATOR_IMAGE_REPOSITORY` |
| RBAC Proxy | `quay.io/brancz/kube-rbac-proxy` | `mharbor-cs.cloud.kemai.cn/public/kube-rbac-proxy:v0.19.1` | `K8SGPT_RBAC_PROXY_IMAGE_REPOSITORY` |
| Engine | `ghcr.io/k8sgpt-ai/k8sgpt` | `mharbor-cs.cloud.kemai.cn/public/k8sgpt:v0.4.41` | `K8SGPT_ENGINE_IMAGE_REPOSITORY` |

在能访问上游仓库和 Harbor 的机器上同步镜像。以下命令按项目基线 digest 选择上游镜像，并将所有架构复制到 Harbor 的对应版本标签。

```bash
mirror=mharbor-cs.cloud.kemai.cn/public
skopeo copy --all \
  docker://ghcr.io/k8sgpt-ai/k8sgpt-operator@sha256:82d0adcce816182bbfbef9f1d535db93ab0901f08d45a3a9e572c6e795c5bfa8 \
  "docker://${mirror}/k8sgpt-operator:v0.2.29"
skopeo copy --all \
  docker://quay.io/brancz/kube-rbac-proxy@sha256:9f21034731c7c3228611b9d40807f3230ce8ed2b286b913bf2d1e760d8d866fc \
  "docker://${mirror}/kube-rbac-proxy:v0.19.1"
skopeo copy --all \
  docker://ghcr.io/k8sgpt-ai/k8sgpt@sha256:1bc581442ac9c3a9e2f9a2f818c2bd84ba476b445d46fcb168073799caa58640 \
  "docker://${mirror}/k8sgpt:v0.4.41"
```

仅配置镜像仓库时，可以在第 2 节安装步骤中使用以下命令：

```bash
K8SGPT_IMAGE_REGISTRY=mharbor-cs.cloud.kemai.cn/public make install
make verify
```

单镜像变量接收 `仓库地址/项目/镜像名`，优先于统一前缀。安装器为覆盖的镜像追加基线版本标签，默认上游镜像继续按基线 digest 拉取。升级或重跑时继续传入相同变量。安装日志会显示三张镜像的完整引用。需要认证的 Harbor 项目应提前配置集群的镜像拉取凭据。Chart 仍由安装主机从 `charts.k8sgpt.ai` 下载并校验。

### 调整 Operator 资源

以下变量适用于首次安装和已有 Release 的升级，作用于 Operator Deployment 的 `manager` 容器。安装器会打印生效值，并校验每项 request 不超过对应 limit。

| 变量 | 作用 | 默认值 |
| --- | --- | --- |
| `K8SGPT_OPERATOR_CPU_REQUEST` | CPU request | `50m` |
| `K8SGPT_OPERATOR_MEMORY_REQUEST` | 内存 request | `128Mi` |
| `K8SGPT_OPERATOR_CPU_LIMIT` | CPU limit | `500m` |
| `K8SGPT_OPERATOR_MEMORY_LIMIT` | 内存 limit | `256Mi` |

仅提高内存上限时，可在使用内网仓库的安装命令中增加 `K8SGPT_OPERATOR_MEMORY_LIMIT`。这里的 `1Gi` 是配置示例，实际值按容器峰值和节点余量确定。

```bash
K8SGPT_IMAGE_REGISTRY=mharbor-cs.cloud.kemai.cn/public \
K8SGPT_OPERATOR_MEMORY_LIMIT=1Gi \
make install
```

同时指定 CPU 和内存时，将需要的变量一并传给安装命令。重跑或升级时复用相同变量。RBAC Proxy 和 Engine 的资源由各自的配置管理。

```bash
kubectl -n k8sgpt-operator-system get deployment -l app.kubernetes.io/name=k8sgpt-operator \
  -o jsonpath='{range .items[*].spec.template.spec.containers[?(@.name=="manager")]}{.resources}{"\n"}{end}'
```

### 配置 Engine 模型与语言

以下参数适用于首次安装和升级，可与内网镜像仓库、Operator 资源参数同时使用。

| 变量 | K8sGPT CR 字段 | 默认值 |
| --- | --- | --- |
| `K8SGPT_AI_MODEL` | `spec.ai.model` | `gpt-4o-mini` |
| `K8SGPT_AI_BASE_URL` | `spec.ai.baseUrl` | 由 OpenAI backend 选择默认端点 |
| `K8SGPT_AI_LANGUAGE` | `spec.ai.language` | `english` |

以下示例将智谱 `glm-5.2`、中文诊断、内网镜像和 Operator 内存上限用于同一次安装或升级。Base URL 填 API 根路径。[智谱 OpenAI 兼容文档](https://docs.bigmodel.cn/cn/guide/develop/openai/introduction)

首次安装或切换 Provider 时，用目标 Provider 的 API Key 创建或更新 `k8sgpt-openai-secret`。`OPENAI_TOKEN` 是安装命令读取凭据的变量名。已有正确凭据时可直接执行安装。

```bash
(
  set -e
  read -rsp 'Provider API key: ' OPENAI_TOKEN
  printf '\n'
  export OPENAI_TOKEN
  make bootstrap-secret
)
```

将示例镜像前缀替换为实际内网仓库和项目路径，并按实际负载设置 Operator `manager` 容器的内存上限。`1Gi` 为示例值。

```bash
export K8SGPT_AI_MODEL=glm-5.2
export K8SGPT_AI_BASE_URL=https://open.bigmodel.cn/api/paas/v4
export K8SGPT_AI_LANGUAGE=Chinese

K8SGPT_IMAGE_REGISTRY=mharbor-cs.cloud.kemai.cn/public \
K8SGPT_OPERATOR_MEMORY_LIMIT=1Gi \
make install && make verify
```

Engine 继续使用 `openai` backend，需能访问配置的端点。仓库的 Engine 出站策略允许 DNS 以及 TCP 443、6443；使用其他端口时需按实际 Provider 配置出站策略。升级时复用这些变量。`make verify` 会核对当前会话中指定的模型、Base URL 和语言是否已写入 CR，实际调用与结果生成在目标集群中验收。

```bash
kubectl -n k8sgpt-operator-system get k8sgpt k8sgpt-engine \
  -o jsonpath='{.spec.version}{"\t"}{.spec.ai.backend}{"\t"}{.spec.ai.model}{"\t"}{.spec.ai.baseUrl}{"\t"}{.spec.ai.language}{"\n"}'
kubectl -n k8sgpt-operator-system get deployment k8sgpt-engine \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="k8sgpt")].image}{"\n"}'
```

等待一次分析周期后，检查分析状态与诊断结果。验收中文时，查看升级后新产生的 Result 中的 `spec.details`。资源名称、事件原文和分析器的错误字段可能仍为英文。

```bash
REQUIRE_ANALYSIS_HEALTH=true make verify
make results
kubectl -n k8sgpt-operator-system get results \
  -l 'k8sgpts.k8sgpt.ai/name=k8sgpt-engine,k8sgpts.k8sgpt.ai/namespace=k8sgpt-operator-system' \
  --sort-by=.metadata.creationTimestamp \
  -o custom-columns='CREATED:.metadata.creationTimestamp,NAME:.metadata.name,AI_DETAILS:.spec.details'
```

### 验证部署

```bash
kubectl -n k8sgpt-operator-system get deployments,pods
kubectl -n k8sgpt-operator-system get k8sgpts.core.k8sgpt.ai k8sgpt-engine
make status
```

在测试集群中生成故障样例。等待至少一次分析周期后查看 Result，再执行严格验收。

```bash
make demo
kubectl -n k8sgpt-demo get pods,pvc
make results
STRICT_RESULTS=true make verify
```

## 3. 准备 API 和 Web 镜像

先创建本次发布目录，保存构建元数据和部署清单。

```bash
revision="$(git rev-parse --short=12 HEAD)"
release_dir="$PWD/.build/deploy/$target_context/$revision"
mkdir -p "$release_dir"
```

已有经过验收的 API 和 Web 镜像时，将完整的 `仓库/镜像@sha256:...` 分别赋给 `api_image` 和 `web_image`，然后进入部署步骤。构建镜像或读取已归档的 Buildx 元数据时，先填写实际发布仓库。

```bash
registry=mharbor-cs.cloud.kemai.cn/kube-aiops
```

按目标节点架构设置 `platforms`，可用 `kubectl get nodes -L kubernetes.io/arch` 查看。混合架构示例为 `linux/amd64,linux/arm64`。API 和 Web 使用同一个已提交的源码版本。含运维知识库 P0/P1 和系统管理新流程的基线为包含 [#103](https://github.com/abchw517/kube-aiops/pull/103) 与 [#104](https://github.com/abchw517/kube-aiops/pull/104) 的提交。

```bash
platforms=linux/amd64
docker login "${registry%%/*}"

docker buildx build --platform "$platforms" --push \
  --metadata-file "$release_dir/api-build.json" -t "$registry/api:$revision" .
docker buildx build --platform "$platforms" --push -f web/Dockerfile \
  --metadata-file "$release_dir/web-build.json" -t "$registry/web:$revision" .
```

Buildx 将发布的 digest 写入元数据，后续 Deployment 使用该 digest。预发验收后归档元数据和发布清单。生产推广时检出同一源码版本，将两个 Buildx 元数据文件恢复到当前环境的 `release_dir`，读取 digest。

```bash
api_digest="$(jq -er '."containerimage.digest" | select(test("^sha256:[0-9a-f]{64}$"))' "$release_dir/api-build.json")"
web_digest="$(jq -er '."containerimage.digest" | select(test("^sha256:[0-9a-f]{64}$"))' "$release_dir/web-build.json")"
api_image="$registry/api@$api_digest"
web_image="$registry/web@$web_digest"
printf '%s\n' "$api_image" "$web_image"
```

## 4. 部署 API 与 Web Portal

创建 Namespace 后，按 [生产身份接入](#生产身份接入) 准备账号和授权配置。

| 清单 | 用途 |
| --- | --- |
| [API Deployment](../deploy/api/deployment.yaml)、[Service](../deploy/api/service.yaml) | API 工作负载和 TCP 8080 ClusterIP Service |
| [API HTTPRoute](../deploy/api/httproute.yaml)、[NetworkPolicy](../deploy/api/networkpolicy.yaml) | `/api/v1` 路由。API 入站允许指定 Gateway 代理和同命名空间 Web Pod |
| [Web Deployment](../deploy/web/deployment.yaml)、[Service](../deploy/web/service.yaml) | Web 工作负载和 TCP 8080 ClusterIP Service |
| [Web HTTPRoute](../deploy/web/httproute.yaml)、[NetworkPolicy](../deploy/web/networkpolicy.yaml) | `/` 路由。Web 入站允许指定 Gateway 代理，出站允许 API TCP 8080 和集群 DNS |

创建 Namespace、API ServiceAccount、只读 RBAC 和 API Service，再按实际镜像渲染两个 Deployment。HTTPRoute 和 NetworkPolicy 在配置 HTTPS 入口时应用。

```bash
kubectl apply -f deploy/api/namespace.yaml
kubectl apply -f deploy/api/serviceaccount.yaml -f deploy/api/clusterrole.yaml \
  -f deploy/api/clusterrolebinding.yaml -f deploy/api/service.yaml
kubectl set image --local -f deploy/api/deployment.yaml "api=$api_image" -o yaml \
  > "$release_dir/api.yaml"

kubectl set image --local -f deploy/web/deployment.yaml "web=$web_image" -o yaml \
  > "$release_dir/web.yaml"
cp deploy/web/service.yaml "$release_dir/web-service.yaml"
```

私有镜像仓库使用 `kube-aiops-system` 中预建的 `kubernetes.io/dockerconfigjson` Secret。在相应 Deployment 的 `spec.template.spec` 下合并以下字段，使用实际 Secret 名称。

```yaml
imagePullSecrets:
  - name: harbor-pull
```

应用清单前，按 [生产身份接入](#生产身份接入) 完成 SQLite PVC 和 `api.yaml` 配置。需要关联指标、日志或告警时，先执行 [配置可观测性来源](#配置可观测性来源)，再应用渲染后的清单。

#### 检查本地节点采集权限

部署或升级 API 后，用有权读取 Deployment 并模拟其 ServiceAccount 的账号核对集群级节点读取权限。默认检查 `kube-aiops-system/kube-aiops-api`，自定义部署可传入 `--namespace` 和 `--deployment`。

```bash
kube_context="$(kubectl config current-context)"
python3 scripts/check-api-node-rbac.py --context "$kube_context"
```

若返回缺少权限，确认目标上下文及服务账号后，由有权管理集群 RBAC 的管理员对同一命令追加 `--repair`。脚本仅向该 API 的专用 ServiceAccount 授予集群级 `list nodes`，并重新检查生效结果。在「资源管理 → 集群接入」对本地自动采集实例点「重新校验」，确认节点列表恢复。

### 启用诊断详情的 AI 分析

完成 [配置 Engine 模型与语言](#配置-engine-模型与语言) 和 [生产身份接入](#生产身份接入) 后，将下列变量合并到 API 容器的 `env` 中：

```yaml
- name: FINDING_AI_ENGINE_ENABLED
  value: "true"
```

可选清单位于 `deploy/examples/`。发布 API 和 Web 时分别选择 `deploy/api/`、`deploy/web/`，按所需功能单独应用示例。

先应用 [Engine 读取权限](../deploy/examples/finding-ai/engine-reader.yaml)，再应用本次 API Deployment：

```bash
kubectl --context "$target_context" apply -f deploy/examples/finding-ai/engine-reader.yaml
kubectl --context "$target_context" apply -f "$release_dir/api.yaml"
kubectl --context "$target_context" -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
```

默认 Role 仅允许 API ServiceAccount 在 `k8sgpt-operator-system` 对 `k8sgpt-engine` 和 `k8sgpt-openai-secret` 执行单对象 `get`。自定义 Engine 名称、命名空间或 Secret 名称时，分别更新 API 的 `K8SGPT_NAMESPACE`、`K8SGPT_NAME` 以及 Role/RoleBinding 的命名空间和 Role 的 `resourceNames`。Kubernetes RBAC 按整个 Secret 授权，Engine 密钥应放在专用 Secret 中。

默认只批准 `https://api.openai.com/v1/chat/completions`。使用智谱等兼容端点时，在 Portal API 的环境变量中设置 `FINDING_AI_ALLOWED_ENDPOINT` 为平台批准的完整 HTTPS 请求地址，例如 `https://open.bigmodel.cn/api/paas/v4/chat/completions`。地址必须与 Engine Base URL 推导出的请求地址完全一致，切换模型供应商时同步更新 API 清单。地址校验在读取 Secret 前执行。

每次提交 AI 分析都会读取现有 Engine 的模型、Base URL 和其引用 Secret 的指定键；轮换同一个 Secret 的值后，下次分析即使用新密钥，无需重启。升级旧部署时，从 API 清单移除原先的 `FINDING_AI_URL`、`FINDING_AI_MODEL`、`FINDING_AI_KEY_FILE` 和 `finding-ai` 卷及挂载。

管理员在诊断详情点击 **AI 分析** 或展开 **AI 复核**，查看模型对关联证据的判断、待验证项和下一步。可以补充现场观察后重新复核。API 在当前诊断的集群、命名空间、资源和时间窗口内查询关联证据，例如 Kubernetes 事件以及已配置的 Prometheus、VictoriaLogs 和 Alertmanager。请勿提交凭据或个人敏感信息。

核对并编辑复核结论后，点击 **保存复核结论**。在同一诊断的 **相关知识 → 沉淀知识** 下，选择 **创建故障案例草稿**，或者关联一份已有的排障手册。若复核结论已被更新，先加载最新版本。草稿经脱敏预览、送审和人工确认后发布，详见 [运维知识库生产试点验收](#运维知识库生产试点验收)。

诊断复核的模型请求默认等待 35 秒。可在 API 容器环境变量中设置 `FINDING_AI_PROVIDER_TIMEOUT`，取值范围为 `5s` 到 `40s`，本地和手动接入实例共用该配置。复核总时限为 50 秒，其中关联证据查询最多占 5 秒。HTTP 响应写入时限为 55 秒，入口代理应为此路由预留至少 60 秒。知识库问答继续受自身 30 秒总时限约束。

诊断分析返回 JSON `504 AI_PROVIDER_TIMEOUT` 时，现场观察内容保留，可直接重新复核。持续超时时查看 API 日志中 `AI provider request failed` 的 `elapsed_ms`、`client_timeout_ms` 和 `upstream_status`。上游状态为 408 或 504 时，排查模型服务或它的上游网关。响应中缺少此错误码时，对照 Portal 入口代理日志和 API 请求日志判断等待发生在哪一层。

复核成功后，Portal 展示 SQLite 中的最新结论。原始 K8sGPT 诊断和已有复核版本可在弹窗中查看，弹窗展示最近五版。具有该诊断读取权限的用户可查看最新结论。


### 应用清单并等待就绪

在应用前检查清单。Web 示例的副本和资源按实际容量调整。

```bash
kubectl apply --dry-run=server -f "$release_dir/api.yaml" -f "$release_dir/web.yaml" -f "$release_dir/web-service.yaml"
kubectl apply -f "$release_dir/api.yaml" -f "$release_dir/web.yaml" -f "$release_dir/web-service.yaml"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-web --timeout=180s
kubectl -n kube-aiops-system get pods,svc
```

## 配置可观测性来源

Kubernetes Events 通过 API 的 Kubernetes RBAC 读取。其余三个来源通过 API Deployment 的环境变量按需启用。填写 Base URL，保留反向代理路径前缀，API 会追加查询路径。

| 来源 | 启用变量 | API 查询路径 |
| --- | --- | --- |
| Prometheus | `PROMETHEUS_URL` | `/api/v1/query`、`/api/v1/query_range` |
| VictoriaLogs | `VICTORIALOGS_URL` | `/select/logsql/stats_query_range` |
| Alertmanager | `ALERTMANAGER_URL` | `/api/v2/alerts` |

### 完整 Deployment 示例

仓库提供完整的 [API Deployment](../deploy/examples/observability/api-deployment.yaml) 和 [Web Deployment](../deploy/examples/observability/web-deployment.yaml) 示例。API 示例配置了三个数据源的集群内 HTTP 地址和集群标签，Web 通过项目 API 获取关联证据。部署前修改镜像、数据源地址和集群标签，并按生产身份接入章节完成 API 认证授权。

```bash
cp deploy/examples/observability/api-deployment.yaml "$release_dir/api-observability-example.yaml"
cp deploy/examples/observability/web-deployment.yaml "$release_dir/web-observability-example.yaml"

# 修改镜像、数据源地址与集群标签后检查并应用
kubectl apply --dry-run=server -f "$release_dir/api-observability-example.yaml" -f "$release_dir/web-observability-example.yaml"
kubectl apply -f "$release_dir/api-observability-example.yaml" -f "$release_dir/web-observability-example.yaml"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-web --timeout=180s
```

当前项目尚未实现 OpenTelemetry SDK 或 OTLP 导出接口。上述清单启用现有的三个数据源查询适配器，OpenTelemetry 接入需要代码实现后再配置。

### 设置数据源地址和集群标签

在 [API Deployment](../deploy/api/deployment.yaml) 的 `spec.template.spec.containers` 中找到 `name: api`，将下列变量添加到已有的 `env` 列表。地址和集群标签值按实际环境填写，只保留需要启用的来源。渲染镜像后，同样可以直接编辑 `$release_dir/api.yaml`。

```yaml
# 添加到 api 容器已有的 env 列表
- name: PROMETHEUS_URL
  value: "http://prometheus.monitoring.svc:9090"
- name: PROMETHEUS_CLUSTER_LABEL_VALUE
  value: "my-k8s-cluster"
- name: VICTORIALOGS_URL
  value: "http://victorialogs.monitoring.svc:9428"
- name: VICTORIALOGS_CLUSTER_LABEL_VALUE
  value: "my-k8s-cluster"
- name: ALERTMANAGER_URL
  value: "http://alertmanager.monitoring.svc:9093"
- name: ALERTMANAGER_CLUSTER_LABEL_VALUE
  value: "my-k8s-cluster"
```

共享数据源时，分别填写对应数据源中实际的 `cluster` 标签值。单集群专用端点可删除或留空这些标签变量。VictoriaLogs 多租户场景使用已配置目标租户的入口。[证据关联设计](phase-2-correlation-design.md)

### 挂载 HTTPS 凭据

上游使用 Bearer token 或私有 CA 时，使用 HTTPS 地址。以下以 Prometheus 为例。先准备 token 和 CA 文件，在目标 Namespace 创建 Secret。

```bash
kubectl -n kube-aiops-system create secret generic kube-aiops-prometheus \
  --from-file=token=/secure/prometheus.token \
  --from-file=ca.crt=/secure/prometheus-ca.pem
```

将以下字段合并到 `api.yaml` 中现有的 `spec.template.spec`。容器 `name: api`、`env` 和 `volumes` 均在原有列表中添加字段。

```yaml
spec:
  template:
    spec:
      containers:
        - name: api
          env:
            - name: PROMETHEUS_URL
              value: "https://prometheus.example.com"
            - name: PROMETHEUS_BEARER_TOKEN_FILE
              value: "/var/run/secrets/kube-aiops/prometheus/token"
            - name: PROMETHEUS_CA_FILE
              value: "/var/run/secrets/kube-aiops/prometheus/ca.crt"
          volumeMounts:
            - name: prometheus-credentials
              mountPath: /var/run/secrets/kube-aiops/prometheus
              readOnly: true
      volumes:
        - name: prometheus-credentials
          secret:
            secretName: kube-aiops-prometheus
```

VictoriaLogs 和 Alertmanager 可以分别创建独立 Secret。按需替换实际文件路径并执行。

```bash
kubectl -n kube-aiops-system create secret generic kube-aiops-victorialogs \
  --from-file=token=/secure/victorialogs.token \
  --from-file=ca.crt=/secure/victorialogs-ca.pem

kubectl -n kube-aiops-system create secret generic kube-aiops-alertmanager \
  --from-file=token=/secure/alertmanager.token \
  --from-file=ca.crt=/secure/alertmanager-ca.pem
```

将以下字段合并到同一个 API Deployment 的 `spec.template.spec`。三个来源同时启用时，Prometheus、VictoriaLogs 和 Alertmanager 的环境变量及挂载分别放在已有的 `env`、`volumeMounts` 和 `volumes` 列表中。

```yaml
spec:
  template:
    spec:
      containers:
        - name: api
          env:
            - name: VICTORIALOGS_URL
              value: "https://victorialogs.example.com"
            - name: VICTORIALOGS_BEARER_TOKEN_FILE
              value: "/var/run/secrets/kube-aiops/victorialogs/token"
            - name: VICTORIALOGS_CA_FILE
              value: "/var/run/secrets/kube-aiops/victorialogs/ca.crt"
            - name: ALERTMANAGER_URL
              value: "https://alertmanager.example.com"
            - name: ALERTMANAGER_BEARER_TOKEN_FILE
              value: "/var/run/secrets/kube-aiops/alertmanager/token"
            - name: ALERTMANAGER_CA_FILE
              value: "/var/run/secrets/kube-aiops/alertmanager/ca.crt"
          volumeMounts:
            - name: victorialogs-credentials
              mountPath: /var/run/secrets/kube-aiops/victorialogs
              readOnly: true
            - name: alertmanager-credentials
              mountPath: /var/run/secrets/kube-aiops/alertmanager
              readOnly: true
      volumes:
        - name: victorialogs-credentials
          secret:
            secretName: kube-aiops-victorialogs
        - name: alertmanager-credentials
          secret:
            secretName: kube-aiops-alertmanager
```

只使用 token 或私有 CA 时，创建对应文件并设置相应的 `*_BEARER_TOKEN_FILE` 或 `*_CA_FILE`。使用系统信任的证书且无需 token 时，只需填写 HTTPS 地址。变更 Secret 后重启 API Pod 以加载证书。

### 应用并验证数据源

首次部署返回第 4 节应用两套工作负载。已有部署使用包含当前镜像、身份和数据源配置的发布清单更新 API。

```bash
kubectl apply --dry-run=server -f "$release_dir/api.yaml"
kubectl apply -f "$release_dir/api.yaml"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
```

HTTPS 入口就绪后，使用同时拥有 Finding 列表和证据读取权限的账号查询已有 Finding。以下命令中的域名替换为实际域名，`/secure/portal-allowed.curl` 替换为平台提供的 curl 配置文件路径。文件中按身份平台的要求配置认证 Header 或 Cookie。API Pod 需要能够访问 DNS、Kubernetes API、身份服务和数据源端口。

先查看 Finding 列表及 ID：

```bash
curl --config /secure/portal-allowed.curl -fsS --max-time 10 \
  'https://kube-aiops.example.com/api/v1/findings' |
  jq '.items[] | {id, namespace, resource, title}'
```

将列表中选定的 ID 替换到下面的 URL。ID 如包含 URL 特殊字符，先执行下方编码命令，将结果替换到 URL 中。

```bash
jq -rn --arg id '替换为Finding ID' '$id | @uri'
```

```bash
curl --config /secure/portal-allowed.curl -fsS --max-time 15 \
  'https://kube-aiops.example.com/api/v1/findings/替换为编码后的Finding ID/correlation' |
  jq '{sources, signalCount: (.signals | length)}'
```

`available` 表示来源可查询。出现 `partial` 或 `unavailable` 时，检查连通性、TLS 和凭据。未启用的来源显示 `disabled`。查询可用但证据为空时，核对资源标签、集群标签和时间窗口。

## 生产部署

新部署先准备已验证的 API/Web 镜像 digest、域名、证书和 Secret 引用，再按下节初始化。已有 Portal 从 [存量部署升级](#存量部署升级) 开始。

### 存量部署升级

1. 归档当前 API/Web 镜像 digest、路由、网络策略、配置和 Secret 引用。用现有备份工具对 `AUTH_SQLITE_PATH` 指向的数据库做一致性备份，例如使用 SQLite 在线备份。也可在停止 API 写入后用存储快照备份整个卷。备份包含账号、LDAP、权限策略、资源目录、诊断复核、知识库和操作审计。若使用卷上的自动生成直连密钥，备份时包含与数据库同目录的 `<SQLite 文件名>.cluster-credentials.key`。已通过 Secret 挂载的直连、LDAP 和 Wiki 密钥保留对应版本。
2. 将备份恢复到隔离的预发 PVC，用新镜像启动单副本 API 验证开库迁移。预发限制到生产 Wiki 和上报写入入口的访问，核对同步计划及来源后再启用需要的试点计划。检查存量用户、LDAP 登录、角色和用户组绑定、审计分页与详情、诊断复核版本、旧知识直链和查询范围。旧知识缺少复核记录时，授权账号仍可浏览，需要重新送审后才会用于推荐与问答。
3. 从同一提交构建 API/Web，复用现有 PVC、`AUTH_ORIGIN` 和密钥挂载，保持 API 单副本和 `Recreate` 策略。正常升级沿用管理员和 LDAP 配置。先 [应用清单并等待就绪](#应用清单并等待就绪)，再执行 [验收、发布与回滚](#验收发布与回滚)。会话保存在 API 进程内，更新后重新登录。

### 生产身份接入

以下步骤适用于全新部署。SQLite 在首次启动时初始化，账号、权限和 LDAP 配置通过 Portal 创建。按顺序完成存储和清单配置、首次改密、本地账号授权，再按需启用 LDAP。

#### 1. 创建 SQLite PVC

API 和 Web 镜像需包含 SQLite 与系统管理功能。先完成 [部署 API 与 Web Portal](#4-部署-api-与-web-portal) 中的镜像渲染，准备本次发布的 `api.yaml`、`web.yaml` 和 `web-service.yaml`，在准备好身份存储和入口配置后再应用 Deployment。

```bash
kubectl apply -f deploy/api/namespace.yaml
kubectl get storageclass
kubectl apply -f deploy/examples/identity/pvc.yaml
kubectl -n kube-aiops-system get pvc kube-aiops-identity
```

示例使用默认 StorageClass，申请 1 GiB 的 `ReadWriteOnce` 卷。没有默认 StorageClass 时，在 PVC 的 `spec.storageClassName` 指定实际名称。若存储类采用 `WaitForFirstConsumer`，PVC 会在 API Pod 调度时绑定。

#### 2. 配置 API 并发布 HTTPS 入口

把以下片段合并到本次发布的 `api.yaml`。保留原有 Deployment 字段、容器镜像、探针和环境变量。将 `AUTH_ORIGIN` 改为浏览器实际访问的 HTTPS 来源，只填写协议、主机和非默认端口。Portal 和 API 使用同一个来源。

```yaml
spec:
  replicas: 1
  strategy:
    type: Recreate
  template:
    spec:
      securityContext:
        fsGroup: 65532
      containers:
        - name: api
          env:
            - name: AUTH_ORIGIN
              value: https://kube-aiops.example.com
            - name: AUTH_SQLITE_PATH
              value: /var/lib/kube-aiops/identity.db
            - name: AUTH_BOOTSTRAP_ADMIN
              value: "true"
          volumeMounts:
            - name: identity-db
              mountPath: /var/lib/kube-aiops
      volumes:
        - name: identity-db
          persistentVolumeClaim:
            claimName: kube-aiops-identity
```

API 以 UID/GID 65532 运行，PVC 挂载目录需要可写。单副本配合 `Recreate` 使用同一个 SQLite 卷。会话有效期为 30 分钟，API 重启后需重新登录。

执行 [应用清单并等待就绪](#应用清单并等待就绪)，然后按 [挂载 HTTPS 入口](#挂载-https-入口) 应用两条 HTTPRoute。初始化期间将 HTTPS 入口限制在部署人员可访问的范围内。用实际域名检查入口。

```bash
kubectl -n kube-aiops-system get pvc kube-aiops-identity
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-web --timeout=180s
curl -fsS https://kube-aiops.example.com/portal-healthz
curl -sS -o /dev/null -w '%{http_code}\n' https://kube-aiops.example.com/api/v1/findings
```

PVC 应为 `Bound`，匿名诊断请求应返回 `401`。

#### 3. 初始化管理员并关闭引导

1. 打开 `https://kube-aiops.example.com`，选择本地账号登录，输入账号 `admin` 和初始密码 `admin123`。Portal 会直接进入改密页面，改密前只能查看登录状态、修改密码或退出。
2. 在首次改密页面输入当前密码和至少 12 字节的新密码。保存后会话立即失效，用新密码重新登录，确认页面出现 **系统管理**。
3. 从本次发布的 `api.yaml` 删除 `AUTH_BOOTSTRAP_ADMIN`，并更新运行中的 Deployment。

```bash
kubectl -n kube-aiops-system set env deployment/kube-aiops-api AUTH_BOOTSTRAP_ADMIN-
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
```

再次用新密码登录，确认管理员密码已持久化。保留 PVC 供后续发布使用。SQLite 已存在管理员时会保留该账号和密码。

#### 4. 创建测试用户并配置权限

1. 在 **资源管理 → 集群接入** 中确认测试实例已启用并能列出命名空间。例如，选用已登记为研发环境的本地或测试实例，并确认存在 `dev` 命名空间。预发布和生产请先为目标命名空间维护业务线归属。
2. 用本地 `admin` 进入 **系统管理 → 用户管理 → 创建访客**，创建 `portal-operator` 并设置临时密码。访客首次登录后完成改密，再用内置「访客只读」角色检查研发和测试的历史诊断。使用独立会话验证该账号无法读取预发布或生产 Finding。本地账号使用预置角色。
3. 如需验证自定义角色，先按下节启用 LDAP，并让测试员工完成首次登录。进入 **系统管理 → 权限配置 → 角色配置**，选择 **新建角色**，填写名称「生产应用诊断观察员」，选择生产实例、业务线和明确命名空间。同一实例和业务线可一次勾选多个命名空间，点击 **添加并预览**，核对增减与引用后 **确认保存**。研发和测试也可授予整个环境或整个实例。
4. 打开 LDAP 员工的用户详情，点击 **分配或撤销 → 直接角色**，或先在 **权限配置 → 用户组** 创建组并关联角色，再给员工分配该组。通过用户详情核对直接和继承权限来源。通过授权结果的 **查看本次审计** 打开对应记录，或从用户详情按账号进入审计。
5. 用独立会话验证该员工可查看已授权的生产诊断列表、汇总和详情。对照同一生产实例的其他命名空间，确认范围外结果不可见。撤销授权后，从该员工的会话重新查询，确认访问随之收回。若同一范围还有其他授权来源，先核对用户详情中的生效权限。

范围角色当前用于历史诊断查看。AI 复核、关联证据、知识库与平台管理按现行接口授权。旧模块策略仍作用于使用该策略的存量身份，对这些身份继续验证已有规则和显式拒绝。知识库试点账号和验证步骤见 [生产试点验收](#运维知识库生产试点验收)。

#### 5. 按需接入 LDAP / Active Directory

准备域控的 LDAPS 域名、只读搜索账号和密码。确认 API Pod 能解析域控域名，并可访问 TCP 636（LDAPS）或 389（明文 LDAP）。网页验证并保存后，LDAP 配置和搜索密码写入身份 SQLite 数据库。

使用私有 CA 时，准备签发域控证书的 CA 链并创建下方 Secret。

```bash
kubectl -n kube-aiops-system create secret generic kube-aiops-ldap \
  --from-file=ca.crt=/secure/ad-ca.crt
```

向 `api.yaml` 追加以下配置，保留 SQLite 挂载。域控证书使用系统受信任的 CA 时，可省略 `AUTH_LDAP_CA_FILE`、`ldap` 卷及挂载。

```yaml
spec:
  template:
    spec:
      containers:
        - name: api
          env:
            - name: AUTH_LDAP_CA_FILE
              value: /etc/kube-aiops/ldap/ca.crt
          volumeMounts:
            - name: ldap
              mountPath: /etc/kube-aiops/ldap
              readOnly: true
      volumes:
        - name: ldap
          secret:
            secretName: kube-aiops-ldap
```

```bash
kubectl apply -f "$release_dir/api.yaml"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
```

管理员重新登录，进入 **系统管理 → 认证配置**，按目录结构填写。填写搜索密码、**LDAP 测试员工账号**和该员工的密码，再点 **运行测试**。测试依次检查目录连接、搜索账号绑定、唯一员工查询和员工本人认证。通过后在 5 分钟内点击 **保存并启用**。修改草稿后须重新验证。测试不创建用户或会话，测试失败时当前配置继续生效。

连接模式由 URL 决定。使用 `ldap://` 时需在页面勾选「允许明文 LDAP」，搜索账号和员工凭据会以明文传输。原来使用 `ldap://` 的已保存配置在升级后需要用本地管理员重新测试并保存。采用环境变量配置时，使用 `AUTH_LDAP_ALLOW_PLAIN=true` 显式启用明文 LDAP。

若 LDAPS 的「目录连接」失败，从 API Pod 的网络环境验证证书。设置 `ldap_host` 为 URL 中的域名，`ldap_ca` 为 API 使用的签发 CA 链文件。使用系统受信任的 CA 时，可省略 `-CAfile`。

```bash
openssl s_client -connect "${ldap_host}:636" -servername "$ldap_host" \
  -verify_hostname "$ldap_host" -verify_return_error -CAfile "$ldap_ca" </dev/null
```

默认无需配置 `AUTH_LDAP_ENCRYPTION_KEY_FILE`，搜索密码直接保存在 SQLite 中。数据库及备份包含可读取的搜索密码，应限制文件访问权限。测试员工密码只用于当次验证。修改 URL 或搜索 DN 时需重新填写搜索密码。

已有加密配置仍可使用原密钥。移除原密钥后，本地管理员可重新填写搜索密码，运行测试并保存，以恢复 LDAP 登录。部署显式提供密钥时，API 仍可对 SQLite 中的搜索密码加密。若外部集群凭据也在复用该密钥，变更挂载前需确保 `CLUSTER_CREDENTIAL_KEY_FILE` 指向相同密钥。

| 字段 | AD 示例 |
| --- | --- |
| LDAP URL | `ldaps://ad.example.com:636` 或 `ldap://ad.example.com:389`（需明确允许明文 LDAP） |
| LDAP 搜索密码 | 搜索账号的密码 |
| LDAP 搜索 DN | `cn=kube-aiops-reader,ou=service,dc=example,dc=com` |
| LDAP 基础 DN | `dc=example,dc=com` |
| LDAP 用户 UID | `sAMAccountName` |
| LDAP 附加用户过滤器 | `(objectClass=user)`，可留空。沿用带 `%s` 的旧模板时请留空 UID |
| LDAP 用户搜索范围 | 子树（默认），可选单层或基础对象 |
| LDAP 组成员属性 | `memberOf` |
| LDAP 组基础 DN | 可选，例如 `OU=Groups,DC=example,DC=com` |
| LDAP 组过滤器 | 可选，例如 `(objectClass=group)` |
| LDAP 组搜索范围 | 默认子树，也可选单层或基础对象 |
| LDAP 测试用户名 | 例如 `alice`，仅用于本次测试 |

在独立浏览器会话选择 **LDAP / Active Directory 登录**，用测试员工验证认证。新目录账号出现在 **系统管理 → 用户管理** 后，由管理员分配平台用户组或直接角色。用该员工复验授权范围，并用未分配自定义角色的员工确认预发布和生产诊断不可见。现有 LDAP 策略使用 `memberOf` 时，应核对原策略中的完整组 DN 和该用户的直接组；平台用户组的角色继承通过 Portal 用户关联管理。LDAP 密码由域控验证。

#### 6. 核对部署结果

```bash
kubectl -n kube-aiops-system get deployment kube-aiops-api kube-aiops-web
kubectl -n kube-aiops-system get pvc kube-aiops-identity
kubectl -n kube-aiops-system logs deployment/kube-aiops-api --tail=100
```

API 和 Web 应各自就绪，PVC 为 `Bound`。若 API 在启动时退出，核对 `AUTH_ORIGIN`、PVC 写权限和引导配置。若 LDAP 登录返回服务不可用，核对域控连通性、证书链、搜索账号凭据和过滤器。完成验收后按发布流程开放入口，并将 PVC 纳入备份。

### 运维知识库生产试点验收

知识库随生产模式 API 和 SQLite 启动，使用同一个 PVC 和 Portal 会话。试点先准备实际集群实例与命名空间，预发布和生产需配置业务线归属。准备两位独立的有权管理员，以及一个普通账号用于权限验证。本地 `admin` 可通过「用户管理 → 创建系统管理员」建立第二个试点审核账号，该账号首次登录时需完成改密。生产范围的发布、扩大已有范围，或首次发布到多个目标，需要由草稿创建者以外的有权管理员确认。

以下场景在目标环境依次演练。

| 场景 | 操作和预期 |
| --- | --- |
| 诊断沉淀 | 在已启用 AI 复核的实例打开 Finding，保存复核结论。进入 **相关知识 → 沉淀知识 → 创建故障案例草稿**，核对来源 Finding、复核版本和适用范围。已有手册可在此关联到指定知识版本。 |
| 审核和发布 | 在草稿中完成必填章节，选择「脱敏预览」并「提交审核」。生产由另一位有权管理员进入 **运维知识库 → 知识治理**，核对后 **确认发布**。草稿送审期间不进入检索。 |
| 检索和问答 | 用症状、报错或事件 Reason 搜索，检查环境、业务线、资源类型和知识类型筛选。打开 **AI 知识问答**，核对引用可跳转到对应文档版本和章节。缺少可信引用时应显示证据不足。问答复用本地 Engine 模型配置，按 [启用 AI 分析](#启用诊断详情的-ai-分析) 设置端点和权限。 |
| 反馈和治理 | 阅读者在版本内提交有用或过时反馈，发布者查看反馈、登记复核或撤回。撤回后复查普通直链、检索、问答与 Finding 推荐。检查发布、复核、撤回的审计记录是否符合风险等级保留策略。 |
| 隔离和升级 | 用普通账号验证超范围诊断和知识不会出现在列表、引用或直链中。对迁移前已发布的条目核对原 ID 与授权范围。 |

#### 可选配置远端 Wiki 同步

当前连接器通过 HTTPS 和 HTTP Basic 读取目录。响应的 `pages` 数组中，每篇文档包含 `id`、`title` 和 Markdown `body`，翻页由 `next` 指定。公司 Wiki 使用不同认证或目录协议时，需先在测试目录确认适配。API Pod 需能访问远端地址并信任其证书。同步为本地候选草稿，按同一流程审核发布。

先在受控运维主机生成独立的 32 字节 Base64 密钥，并创建 Secret。已有同步部署直接复用原密钥。

```bash
umask 077
wiki_key_file=/secure/kube-aiops-wiki-credential-key
openssl rand -base64 32 > "$wiki_key_file"
kubectl --context "$target_context" -n kube-aiops-system create secret generic kube-aiops-wiki-credential-key \
  --from-file=key="$wiki_key_file"
```

把以下片段合并到 `api.yaml`，保留原有身份存储、环境变量和挂载。

```yaml
spec:
  template:
    spec:
      containers:
        - name: api
          env:
            - name: WIKI_CREDENTIAL_ENCRYPTION_KEY_FILE
              value: /etc/kube-aiops/wiki/key
          volumeMounts:
            - name: wiki-credential-key
              mountPath: /etc/kube-aiops/wiki
              readOnly: true
      volumes:
        - name: wiki-credential-key
          secret:
            secretName: kube-aiops-wiki-credential-key
```

应用 API 清单并等待就绪。进入 **运维知识库 → 远端 Wiki 同步**，先由管理员填写来源编号、目录 URL 和目标实例 / 命名空间，点击 **注册来源**。执行人填写自己的只读 Wiki 账号，**测试连接 → 保存绑定 → 立即同步**。在执行记录检查逐页结果及最近整源成功时间，分别验证单页失败重试与整源重试。试点通过后再按需保存定时计划，后台计划使用可重新校验权限的本地身份。保留数据库和该密钥的配套备份。

接口与验收用例见 [运维知识库 P0/P1 接口与上线验收](operations-knowledge/api-contract.md)。

### 挂载 HTTPS 入口

由平台在目标 Gateway 的命名空间准备签发给 `portal_host` 的 TLS Secret。以下是可合并到目标 Gateway 的 `spec.listeners` 中的监听器示例。使用实际证书 Secret 名称，已有共享 listener 可沿用平台的证书和命名空间授权配置。配置方式见 [Gateway API TLS](https://gateway-api.sigs.k8s.io/guides/user-guides/tls/) 和 [跨命名空间挂载](https://gateway-api.sigs.k8s.io/guides/user-guides/multiple-ns/)。

```yaml
- name: https
  hostname: kube-aiops.example.com
  protocol: HTTPS
  port: 443
  tls:
    mode: Terminate
    certificateRefs:
      - kind: Secret
        name: kube-aiops-tls
  allowedRoutes:
    namespaces:
      from: Selector
      selector:
        matchLabels:
          kubernetes.io/metadata.name: kube-aiops-system
```

按环境将 `portal_host` 解析到 Gateway 地址。两个 HTTPRoute 使用同一个域名和 HTTPS listener。API 路由将 `/api/v1` 转发到 API Service，Web 路由将 `/` 转发到 Web Service。

将 [API](../deploy/api) 和 [Web](../deploy/web) 的 HTTPRoute 与 NetworkPolicy 复制到本次发布目录。

```bash
cp deploy/api/httproute.yaml "$release_dir/api-route.yaml"
cp deploy/web/httproute.yaml "$release_dir/web-route.yaml"
cp deploy/api/networkpolicy.yaml "$release_dir/api-networkpolicy.yaml"
cp deploy/web/networkpolicy.yaml "$release_dir/web-networkpolicy.yaml"
```

在两份 Route 中填写实际 Gateway 名称、命名空间、listener 名称和域名。集群需要支持 NetworkPolicy 的 CNI。根据实际 Gateway 数据面 Pod 的命名空间和标签调整两份策略的入站选择器。

| 流量 | 允许范围 |
| --- | --- |
| Gateway → Web / API | 指定 Gateway 数据面 Pod → TCP 8080 |
| Web → API | 同命名空间的 Web Pod → API Pod TCP 8080。Web Pod 内可通过 `http://kube-aiops-api:8080` 访问 API Service |
| Web → DNS | `kube-system` 中标签为 `k8s-app=kube-dns` 的 DNS Pod，UDP/TCP 53 |

使用 NodeLocal DNSCache 或自定义 DNS 时，应用前根据 Web Pod 的 `/etc/resolv.conf` 和 CNI 的 DNS 策略调整 Web 出站规则。API 的出站由集群现有策略管理，需允许 DNS、Kubernetes API 和已启用的上游。

应用策略前确认 API 已在 `production` 模式成功启动。用实际值设置以下变量并核对 Gateway 与数据面 Pod。

```bash
portal_host=kube-aiops.example.com
gateway_namespace=envoy-gateway-system
gateway_name=production-gateway
proxy_namespace=envoy-gateway-system

kubectl -n "$gateway_namespace" wait --for=condition=Programmed "gateway/$gateway_name" --timeout=120s
kubectl -n "$gateway_namespace" get gateway "$gateway_name" -o jsonpath='{.status.addresses[*].value}{"\n"}'
kubectl -n "$proxy_namespace" get pods \
  -l "gateway.envoyproxy.io/owning-gateway-namespace=$gateway_namespace,gateway.envoyproxy.io/owning-gateway-name=$gateway_name"

kubectl apply --dry-run=server -f "$release_dir/api-networkpolicy.yaml" -f "$release_dir/web-networkpolicy.yaml" \
  -f "$release_dir/api-route.yaml" -f "$release_dir/web-route.yaml"
kubectl apply -f "$release_dir/api-networkpolicy.yaml" -f "$release_dir/web-networkpolicy.yaml"
kubectl apply -f "$release_dir/api-route.yaml" -f "$release_dir/web-route.yaml"
kubectl -n kube-aiops-system get httproute kube-aiops-api kube-aiops-web -o yaml
```

核对两条 Route 在目标 listener 下的 `Accepted=True`、`ResolvedRefs=True`，以及状态所对应的当前 generation。若此前应用了旧版合并路由 `httproute/kube-aiops`，确认其归属及两条新 Route 均已就绪后，删除旧 Route，再执行下节验收。若使用预发域名，在完成验收后再切生产 DNS 或流量。

### 验收、发布与回滚

先验证 TLS、页面和 API 访问控制。对于将未认证请求转交给 API 的入口，下面的请求应返回 `401`；若入口统一跳转登录，在预发通过受控测试路径复验应用层 `401`。

```bash
portal_url="https://$portal_host"
curl -fsS --max-time 10 "$portal_url/portal-healthz"
curl -fsS --max-time 10 -o /dev/null "$portal_url/"
test "$(curl -sS --max-time 10 -o /dev/null -w '%{http_code}' \
  "$portal_url/api/v1/findings")" = 401
```

使用已获准的登录会话在浏览器完成列表、详情和证据验收。自动验收时，可将认证 Header 或 Cookie 交由受控的 curl 配置文件提供。`allowed_curl_config` 和 `denied_curl_config` 分别对应允许和缺少 `findings:list` 权限的已认证账号。

```bash
curl --config "$allowed_curl_config" -fsS --max-time 10 "$portal_url/api/v1/findings" |
  jq -e '.items | type == "array"' >/dev/null
test "$(curl --config "$denied_curl_config" -sS --max-time 10 -o /dev/null -w '%{http_code}' \
  "$portal_url/api/v1/findings")" = 403
```

| 检查 | 放行标准 |
| --- | --- |
| 认证和授权 | 具备目标能力的账号读取列表、详情和关联证据成功；无权账号对同一受保护资源收到 `403`；跨命名空间请求无法读取范围外的数据 |
| K8sGPT 和证据 | `make verify` 通过；用实际 Result 对照 Finding 详情，验证已启用数据源及其状态 |
| 系统管理 | 访客首次改密，直授和组继承可核对，角色多选与预览可保存，授权结果可打开审计详情 |
| 运维知识库 | 草稿、送审、发布、版本引用和撤回通过预发验收。启用问答或远端同步时完成真实联调 |
| 审计 | API 审计记录能关联到同一次请求，日志中无明文凭据 |
| 稳定性 | API 和 Web rollout 完成，探针稳定；Operator 无新增 OOM 重启，主要请求的错误率和延迟符合本次发布标准 |

生产沿用预发已验证的清单和 digest。保留本次以及上一版的 `api.yaml`、`web.yaml`、`web-service.yaml`、两份 Route、两份 NetworkPolicy、镜像 digest 和身份策略版本。出现故障时，先核对上一版能否读取升级后的数据库，以及能否正确执行已撤回、停用和授权变更。回退前暂停知识发布、同步和授权变更，保留当前数据库快照。若需恢复升级前备份，先核对发布窗口内新增版本、撤回、权限和审计变更，并在重新开放流量前复验，避免旧版本重新分发已撤回知识。确认数据兼容或完成受控恢复后，再恢复上一版清单与策略。若上一版仍使用合并的 `route.yaml`，按实际归档恢复上一版清单和网络策略。确认旧 Route 已被 Gateway 接受后，移除本次新建的两条 Route，复验旧入口。

```bash
previous=/path/to/previous-approved-release
manifests=(
  -f "$previous/api.yaml" -f "$previous/web.yaml" -f "$previous/web-service.yaml"
  -f "$previous/api-networkpolicy.yaml" -f "$previous/web-networkpolicy.yaml"
  -f "$previous/api-route.yaml" -f "$previous/web-route.yaml"
)
kubectl apply --dry-run=server "${manifests[@]}"
kubectl apply "${manifests[@]}"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-web --timeout=180s
make verify
```

首次发布且尚无上一版时，撤销本次新增的流量入口并保留工作负载供排查。K8sGPT 安装过程中的失败由安装器按 Lease 和快照处理回滚。若安装已成功但需回退配置，使用上一版获批的源码和参数重新执行 `make install`，然后运行 `make verify`。安装器报告回滚失败时，先按日志给出的快照和恢复报告检查 Helm、RBAC 与 K8sGPT CR 状态。

## 排查与清理

| 现象 | 核对项 |
| --- | --- |
| 下载依赖失败 | 主机能否通过 HTTPS 访问 `go.dev`、`nodejs.org`、`dl.k8s.io`、`get.helm.sh` 及下载重定向地址。也可提前安装工具并加入 `PATH` |
| 安装提示缺少 Secret | 当前 context、目标 Namespace 以及 `make bootstrap-secret` 的输出 |
| Helm 等待就绪，Pod 出现 `ErrImagePull` 或 `ImagePullBackOff` | 安装日志中的镜像引用、Pod 事件，以及 Pod 所在节点到对应仓库的连通性、TLS 和拉取凭据 |
| Engine 已就绪但 Finding 为空 | 分析周期、Provider 凭据、K8sGPT Result 及 Result 的实例标签 |
| API 的 `/readyz` 返回 503 | Result CRD、K8sGPT Namespace 和 API ServiceAccount 的 Result 读取权限 |
| 页面打开但 API 返回 HTML 或 404 | 同源入口的 `/api/v1/` 转发路径和 API Service 端口 |
| 新功能入口缺失或返回 404 | 对照发布记录检查 API 和 Web 是否来自同一提交。确认两个 Deployment 的运行镜像 digest 与预期一致。再核对页面入口、身份权限和 API 是否启用了 SQLite |
| 诊断显示「范围待确认」 | 检查实例环境、目标命名空间是否可发现，以及预发/生产命名空间是否有有效业务线归属 |
| Wiki 问答或同步失败 | 问答核对本地 Engine 复核开关、端点和 RBAC。同步核对连接器协议、HTTPS 证书、密钥挂载、本人绑定和目标范围，检查执行记录的单页状态 |
| LDAP 配置无法保存 | 在浏览器 Network 检查 `PUT /api/v1/admin/ldap`。成功应返回 204，测试请求应返回 JSON。更换 LDAP 地址或搜索 DN 后重新填写密码，并确认 API 使用 SQLite 身份存储 |
| 证据来源为 `disabled` | 对应 `*_URL` 是否已设置 |
| 证据来源为 `partial` 或 `unavailable` | Pod 到上游的连通性、TLS、凭据、集群标签和查询窗口 |
| API 在生产模式退出 | 后端启动日志中的安全组合校验及可信认证、授权接入情况 |

清理 Portal 时，先下线两条专用 HTTPRoute，再删除本次发布的 Deployment、Service 和 NetworkPolicy。按发布清单核对资源归属后再删除；共享 Gateway、证书和其他系统的资源由平台维护。

在已有测试集群中，`make clean-demo` 清理故障样例，`make uninstall` 清理 K8sGPT Release、实例和项目 RBAC。默认保留 Secret、Namespace 和 CRD。使用 `PURGE_SECRET=true make uninstall` 可同时删除 Provider Secret。

## 接入多个 Kubernetes 集群

现有集群继续使用 `local`。中心 API Pod 需要能通过内网访问各目标集群的 kube-apiserver。目标集群独立运行 K8sGPT，并为中心 API 提供单独的只读凭据。每个目标集群的 RBAC 需要覆盖实际使用的 Result、Event、Namespace 和资源详情读取操作。

创建 `clusters.json`，示例中的 API 地址、实例名、CA 和 Token 路径均按现场替换。

```json
[
  {
    "id": "dev-cn-01",
    "name": "开发集群 01",
    "environment": "dev",
    "apiURL": "https://dev-k8s.example.com:6443",
    "tokenFile": "/etc/kube-aiops/clusters/dev-cn-01/token",
    "caFile": "/etc/kube-aiops/clusters/dev-cn-01/ca.crt",
    "k8sGPTNamespace": "k8sgpt-operator-system",
    "k8sGPTName": "k8sgpt-engine",
    "prometheus": {
      "url": "https://prometheus.example.com",
      "clusterLabelValue": "dev-cn-01"
    }
  }
]
```

```bash
kubectl -n kube-aiops-system create configmap kube-aiops-clusters --from-file=clusters.json
kubectl -n kube-aiops-system create secret generic kube-aiops-dev-cn-01 \
  --from-file=token=/secure/dev-cn-01/token --from-file=ca.crt=/secure/dev-cn-01/ca.crt
```

将以下片段合并到 API Deployment。远端 Token 由平台签发及轮换，更新时保留整个 Secret 目录挂载。

```yaml
spec:
  template:
    spec:
      containers:
        - name: api
          env:
            - name: KUBERNETES_CLUSTERS_FILE
              value: /etc/kube-aiops/registry/clusters.json
          volumeMounts:
            - name: cluster-registry
              mountPath: /etc/kube-aiops/registry
              readOnly: true
            - name: dev-cluster-credentials
              mountPath: /etc/kube-aiops/clusters/dev-cn-01
              readOnly: true
      volumes:
        - name: cluster-registry
          configMap:
            name: kube-aiops-clusters
        - name: dev-cluster-credentials
          secret:
            secretName: kube-aiops-dev-cn-01
```

每个远端可独立配置 Prometheus、VictoriaLogs 和 Alertmanager。启用任一远端集群后，所有已启用的可观测性来源均需配置集群标签，`local` 使用原有的 `*_CLUSTER_LABEL_VALUE`。共享数据源中的集群标签必须互不相同。保存集群配置后重新部署 API。

在权限策略里保留全局 `clusters:list` 用于访问集群列表接口，再用具体集群规则限制可见性及数据访问。由于通配规则也匹配各集群，可以对受限组为指定集群添加 `clusters:list` 拒绝规则，拒绝优先。接入生产前，检查已有 `cluster: "*"` 读取授权。诊断详情及证据链接可使用 `?cluster=dev-cn-01`，省略该参数仍访问 `local`。

从两个非生产集群分别查看同名 Result、资源及证据，确认来源正确。断开一个远端集群后，另一个集群的查询和中心 API readiness 应继续工作。

### Web 管理多环境集群接入

使用生产模式和 SQLite，打开 **资源管理 → 集群接入**。每个实例使用不可变的实例 ID，填写环境、业务线、集群名称和实例显示名。例如 `生产 / 云帆 / 云帆应用集群 / 上海`，实例 ID 为 `prod-yunfan-sh01`。诊断台按这四级选择实例，详情及证据链接携带实例 ID。通过文件注册的旧实例继续可用，其 ID 不可被 Web 重复使用。

**外部集群直连**：在「新增集群」填写 HTTPS API 地址、Engine 名称及命名空间。选择使用内嵌 CA 和静态凭据的 kubeconfig、专用只读 ServiceAccount Token 加 CA，或客户端证书私钥加 CA。先点「测试连接」，成功后保存并校验。凭据加密存入 SQLite。部署可挂载稳定的 `CLUSTER_CREDENTIAL_KEY_FILE`，已有部署保留原密钥或卷上的同库密钥文件。中心的出站访问应限制在受管集群和经批准的数据源。手动实例可以配置 Prometheus、VictoriaLogs 和 Alertmanager，集群标签值使用实例 ID；共享数据源的标签需唯一。

**采集上报**：在 Web 预登记实例并启用，保存后会显示一次实例上报凭据。将凭据写入受控文件，在目标集群的 Engine 命名空间创建采集 Secret：

```bash
kubectl -n k8sgpt-operator-system create secret generic kube-aiops-collector \
  --from-file=token=/secure/collector-token
```

拷贝 [采集 CronJob 示例](../deploy/collector/cronjob.yaml)，替换 API 镜像、中心 HTTPS 地址、实例 ID 和 Engine 参数，再应用到目标集群。实例首次提交快照后显示在线，每分钟上报当前有效 Result。完整空快照表示当前无诊断，采集失败保留最后一份快照；超过三分钟未收到快照标记为离线。中心只保存凭据哈希，停用实例可阻止查询和继续上报。采集组件只读取指定 Engine 命名空间的 Results，不上传 Kubernetes 凭据。采集实例的 Kubernetes 资源详情和原生 Event 尚未上报，可配置独立指标、日志、告警证据源。

**权限与 AI**：按环境与实例配置诊断角色范围。上线前一并核对存量模块策略中的通配授权。手动接入实例可勾选「启用 AI 复核」，复核时使用该实例的 Kubernetes 连接读取配置的 Engine 和其引用的 Secret。需在目标集群为中心 API 使用的凭据授予对指定 Engine 和 Secret 的单对象 `get`，可参考 [Engine 读取权限](../deploy/examples/finding-ai/engine-reader.yaml) 并调整绑定主体。采集上报模式没有目标 Kubernetes API 连接，因此不支持此项。远端实例默认沿用中心 API 的 `FINDING_AI_ALLOWED_ENDPOINT`，可用 `FINDING_AI_ALLOWED_ENDPOINT_<实例 ID 大写并将短横线改为下划线>` 单独配置，例如 `FINDING_AI_ALLOWED_ENDPOINT_PROD_YUNFAN_SH01`。两者均未设置时只允许 OpenAI 默认地址。既有 `local` 使用 `FINDING_AI_ENGINE_ENABLED=true` 启用。升级时移除旧的 `aiURL`、`aiModel` 配置及 `ai-key` 挂载，并在手动接入实例中重新启用。新增实例后，检查显式实例授权是否覆盖。预发布和生产需维护目标命名空间的业务线归属。
