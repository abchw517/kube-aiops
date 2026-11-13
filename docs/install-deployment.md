# kube-aiops 安装与部署

在已有 Kubernetes 集群中按顺序完成 [预检](#1-依赖和预检) → [安装 K8sGPT](#2-安装-k8sgpt) → [准备镜像](#3-准备-api-和-web-镜像) → [部署 API 与 Web Portal](#4-部署-api-与-web-portal)。已有 K8sGPT 部署时，先核对其 Namespace、实例名和 Result。API 清单默认读取 `k8sgpt-operator-system/k8sgpt-engine`，使用其他实例时，按实际配置调整 API 清单中的 `K8SGPT_NAMESPACE` 和 `K8SGPT_NAME`，并核对 ServiceAccount 的读取权限。

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

按目标节点架构设置 `platforms`，可用 `kubectl get nodes -L kubernetes.io/arch` 查看。混合架构示例为 `linux/amd64,linux/arm64`。对外发布使用已提交身份集成的源码版本。

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

先完成 [生产身份接入](#生产身份接入)，准备账号和授权配置。

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

在 `api.yaml` 中配置身份适配器所需的变量和 Secret 引用。需要关联指标、日志或告警时，先执行 [配置可观测性来源](#配置可观测性来源)，再应用渲染后的清单。

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

配置本地账号或 LDAP 后，从同一源码构建并发布生产镜像，再按前面的现有集群步骤部署。使用已验证的镜像 digest、发布清单和权限策略，按目标环境配置域名、证书和 Secret 引用。

### 生产身份接入

API 支持本地账号和 LDAPS，使用授权策略控制访问。Portal 和 API 必须通过同一个 HTTPS 域名访问。会话保存在 API 进程内，有效期 30 分钟，重启后需要重新登录。当前配置使用单副本，滚动更新期间也应避免同时运行多个副本。

1. 创建本地账户文件。使用 `htpasswd -nBC 12 portal-admin` 交互生成 bcrypt 密码散列，将冒号后面的完整散列填入 `/secure/accounts.json`。

```json
[{"username":"portal-admin","passwordHash":"替换为bcrypt散列","groups":["operators"]}]
```

2. 创建 `/secure/policy.json`，按实际集群和命名空间修改授权。LDAP 组使用小写完整 DN，`memberOf` 返回直接组。

```json
{
  "version": "v1",
  "rules": [{
    "effect": "allow",
    "subjects": ["local:portal-admin"],
    "groups": ["ldap:cn=kube-aiops-readers,ou=groups,dc=example,dc=com"],
    "capabilities": ["clusters:list", "namespaces:list", "findings:list", "findings:summary", "findings:read", "resources:read", "correlations:read"],
    "scopes": [{"cluster": "*"}]
  }]
}
```

3. 创建 Secret。文件包含凭据，应限制文件和 Kubernetes Secret 的读取权限。

```bash
kubectl -n kube-aiops-system create secret generic kube-aiops-identity \
  --from-file=accounts.json=/secure/accounts.json \
  --from-file=policy.json=/secure/policy.json
```

4. 将以下配置合并到 API Deployment。按环境修改域名。密码或策略变更后重启 API。

```yaml
spec:
  replicas: 1
  strategy:
    type: Recreate
  template:
    spec:
      containers:
        - name: api
          env:
            - name: AUTH_ORIGIN
              value: https://kube-aiops.example.com
            - name: AUTH_LOCAL_ACCOUNTS_FILE
              value: /etc/kube-aiops/identity/accounts.json
            - name: AUTH_POLICY_FILE
              value: /etc/kube-aiops/identity/policy.json
          volumeMounts:
            - name: identity
              mountPath: /etc/kube-aiops/identity
              readOnly: true
      volumes:
        - name: identity
          secret:
            secretName: kube-aiops-identity
```

5. 使用 AD 时，准备只读查询账号密码和签发域控证书的 CA。确认 API NetworkPolicy 放行域控 636 端口，并将 CA 对应的域名用于 LDAPS 地址。

```bash
kubectl -n kube-aiops-system create secret generic kube-aiops-ldap \
  --from-file=bind-password=/secure/ldap-bind-password \
  --from-file=ca.crt=/secure/ad-ca.crt
```

在上述容器 `env`、`volumeMounts` 和 Pod `volumes` 中分别追加以下条目。

```yaml
# env
- name: AUTH_LDAP_URL
  value: ldaps://ad.example.com:636
- name: AUTH_LDAP_BASE_DN
  value: dc=example,dc=com
- name: AUTH_LDAP_BIND_DN
  value: cn=kube-aiops-reader,ou=service,dc=example,dc=com
- name: AUTH_LDAP_USER_FILTER
  value: '(&(objectCategory=person)(objectClass=user)(sAMAccountName=%s))'
- name: AUTH_LDAP_GROUP_ATTRIBUTE
  value: memberOf
- name: AUTH_LDAP_BIND_PASSWORD_FILE
  value: /etc/kube-aiops/ldap/bind-password
- name: AUTH_LDAP_CA_FILE
  value: /etc/kube-aiops/ldap/ca.crt
```

```yaml
# volumeMounts
- name: ldap
  mountPath: /etc/kube-aiops/ldap
  readOnly: true
```

```yaml
# volumes
- name: ldap
  secret:
    secretName: kube-aiops-ldap
```

部署后访问 HTTPS Portal，选择本地账号或 LDAP 登录。未配置认证或策略时，生产 API 会拒绝启动。LDAP 和 AD 的目录结构、证书及组映射需要在目标环境验证。

```bash
go test ./cmd/api ./internal/identity ./internal/authorization ./internal/security ./internal/httpapi
make api-contract-check
```

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
| 审计 | API 审计记录能关联到同一次请求，日志中无明文凭据 |
| 稳定性 | API 和 Web rollout 完成，探针稳定；Operator 无新增 OOM 重启，主要请求的错误率和延迟符合本次发布标准 |

生产沿用预发已验证的清单和 digest。保留本次以及上一版的 `api.yaml`、`web.yaml`、`web-service.yaml`、两份 Route、两份 NetworkPolicy、镜像 digest 和身份策略版本。出现故障时，恢复上一版相互兼容的身份策略与发布清单，再复验。若上一版仍使用合并的 `route.yaml`，按实际归档恢复上一版清单和网络策略。确认旧 Route 已被 Gateway 接受后，移除本次新建的两条 Route，复验旧入口。

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
| 证据来源为 `disabled` | 对应 `*_URL` 是否已设置 |
| 证据来源为 `partial` 或 `unavailable` | Pod 到上游的连通性、TLS、凭据、集群标签和查询窗口 |
| API 在生产模式退出 | 后端启动日志中的安全组合校验及可信认证、授权接入情况 |

清理 Portal 时，先下线两条专用 HTTPRoute，再删除本次发布的 Deployment、Service 和 NetworkPolicy。按发布清单核对资源归属后再删除；共享 Gateway、证书和其他系统的资源由平台维护。

在已有测试集群中，`make clean-demo` 清理故障样例，`make uninstall` 清理 K8sGPT Release、实例和项目 RBAC。默认保留 Secret、Namespace 和 CRD。使用 `PURGE_SECRET=true make uninstall` 可同时删除 Provider Secret。
