# kube-aiops 安装与部署

在已有 Kubernetes 集群中按顺序完成 [预检](#1-依赖和预检) → [安装 K8sGPT](#2-安装-k8sgpt) → [准备镜像](#3-准备-api-和-web-镜像) → [部署 API 与 Web Portal](#4-部署-api-与-web-portal)。已有 K8sGPT 部署时，先核对其 Namespace、实例名和 Result。API 清单默认读取 `k8sgpt-operator-system/k8sgpt-engine`，使用其他实例时，按实际配置调整 API 清单中的 `K8SGPT_NAMESPACE` 和 `K8SGPT_NAME`，并核对 ServiceAccount 的读取权限。

| 访问方式 | 使用场景 | 后续步骤 |
| --- | --- | --- |
| `port-forward` | 在现有集群中验证真实数据和 Portal，访问限于有转发权限的运维人员 | [受限访问配置](#仅端口转发模式配置受限访问) |
| `production` | 通过 HTTPS 向已授权用户提供 Portal | [生产部署](#生产部署) |

## 1. 依赖和预检

安装端准备 Git、Make、Bash、Python 3、PyYAML、curl、tar、sha256sum 和可访问目标集群的 kubeconfig。`make cluster-deps` 检查并安装缺少的 kubectl 和 Helm 3。构建镜像需要 Docker Buildx、jq 和仓库推送权限。节点需能拉取所用镜像，Engine Pod 需能访问 AI Provider。版本基线见 [platform-versions.env](../config/platform-versions.env)。

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
test "$(kubectl config current-context)" = "$target_context"
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

| 镜像 | 默认仓库 | 使用 `harbor.example.com/public` 时 | 单独覆盖的变量 |
| --- | --- | --- | --- |
| Operator | `ghcr.io/k8sgpt-ai/k8sgpt-operator` | `harbor.example.com/public/k8sgpt-operator:v0.2.29` | `K8SGPT_OPERATOR_IMAGE_REPOSITORY` |
| RBAC Proxy | `quay.io/brancz/kube-rbac-proxy` | `harbor.example.com/public/kube-rbac-proxy:v0.19.1` | `K8SGPT_RBAC_PROXY_IMAGE_REPOSITORY` |
| Engine | `ghcr.io/k8sgpt-ai/k8sgpt` | `harbor.example.com/public/k8sgpt:v0.4.41` | `K8SGPT_ENGINE_IMAGE_REPOSITORY` |

在能访问上游仓库和 Harbor 的机器上同步镜像。以下命令按项目基线 digest 选择上游镜像，并将所有架构复制到 Harbor 的对应版本标签。

```bash
mirror=harbor.example.com/public
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
K8SGPT_IMAGE_REGISTRY=harbor.example.com/public make install
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
K8SGPT_IMAGE_REGISTRY=harbor.example.com/public \
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
test "$(kubectl config current-context)" = "$target_context"
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

K8SGPT_IMAGE_REGISTRY=harbor.example.com/public \
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
registry=harbor.example.com/kube-aiops
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

Portal 默认通过端口转发验收。通过 HTTPS 发布时，先完成 [生产身份接入](#生产身份接入)，并将 `portal_access` 设为 `production`。按步骤应用指定清单，两个目录中的 HTTPRoute 和生产入站策略在配置 HTTPS 入口时应用。

应用仓库提供的 Namespace、ServiceAccount、只读 RBAC 和 API ClusterIP Service。以仓库中的 API、Web Deployment 为模板，将镜像替换为准备好的 digest。

```bash
portal_access=port-forward
[[ "$portal_access" == port-forward || "$portal_access" == production ]]
test "$(kubectl config current-context)" = "$target_context"
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

在生产模式下，将身份适配器所需的配置和 Secret 引用写入 `api.yaml`。按需接入 [Prometheus、VictoriaLogs 和 Alertmanager](#配置可观测性来源)。例如使用预建 Secret `kube-aiops-prometheus` 中的 `token` 和 `ca.crt`，可向 API Pod 模板合并以下字段，并将 URL 替换为实际 HTTPS 地址。

```yaml
spec:
  template:
    spec:
      containers:
        - name: api
          env:
            - name: PROMETHEUS_URL
              value: https://prometheus.monitoring.svc:9090
            - name: PROMETHEUS_BEARER_TOKEN_FILE
              value: /var/run/secrets/kube-aiops/prometheus/token
            - name: PROMETHEUS_CA_FILE
              value: /var/run/secrets/kube-aiops/prometheus/ca.crt
          volumeMounts:
            - name: prometheus-credentials
              mountPath: /var/run/secrets/kube-aiops/prometheus
              readOnly: true
      volumes:
        - name: prometheus-credentials
          secret:
            secretName: kube-aiops-prometheus
```

对开启网络隔离的集群，允许入口访问两个后端，并按实际配置允许 API Pod 访问 DNS、Kubernetes API、身份端点和已启用的数据源。

### 仅端口转发模式配置受限访问

该模式用于在现有集群验收未接入企业身份的 API 镜像。使用支持 NetworkPolicy 的 CNI，限制专用命名空间的 Pod 创建权限和 `pods/portforward` 权限。先确认两个 Service 均未被已有 Ingress、HTTPRoute、NodePort 或其他入口公开，且没有额外允许 API 或 Web 入站的策略。若此前应用了生产模式的两份入站策略，先撤下生产路由和这两份策略。该模式下 API 使用 `SECURITY_MODE=development`，对外发布走 [生产部署](#生产部署)。

下列配置让 Web 代理同源 `/api/v1/`，拒绝常规 Pod 流量直接访问 Web，并允许同命名空间中的 Web Pod 访问 API。运维访问使用绑定本机回环地址的 Kubernetes 端口转发。

```bash
test "$portal_access" = port-forward

cat > "$release_dir/restricted-ingress.yaml" <<'YAML'
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kube-aiops-web-restricted
  namespace: kube-aiops-system
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: kube-aiops-web
  policyTypes: [Ingress]
  ingress: []
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kube-aiops-api-from-web
  namespace: kube-aiops-system
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: kube-aiops-api
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: kube-aiops-web
      ports:
        - protocol: TCP
          port: 8080
YAML
kubectl apply --dry-run=server -f "$release_dir/restricted-ingress.yaml"
kubectl apply -f "$release_dir/restricted-ingress.yaml"

awk '
  $0 == "    location / {" {
    print "    location /api/v1/ {"
    print "      proxy_pass http://kube-aiops-api:8080;"
    print "    }"
  }
  { print }
' web/nginx.conf > "$release_dir/nginx.conf"
kubectl -n kube-aiops-system create configmap kube-aiops-web-restricted \
  --from-file=nginx.conf="$release_dir/nginx.conf" --dry-run=client -o yaml |
  kubectl apply -f -

kubectl set env --local -f "$release_dir/api.yaml" SECURITY_MODE=development -o yaml \
  > "$release_dir/api-restricted.yaml"
mv "$release_dir/api-restricted.yaml" "$release_dir/api.yaml"
cat > "$release_dir/web-proxy-patch.yaml" <<'YAML'
spec:
  template:
    spec:
      containers:
        - name: web
          volumeMounts:
            - name: nginx-config
              mountPath: /etc/nginx/nginx.conf
              subPath: nginx.conf
              readOnly: true
      volumes:
        - name: nginx-config
          configMap:
            name: kube-aiops-web-restricted
YAML
kubectl patch --local -f "$release_dir/web.yaml" --type=strategic \
  --patch-file "$release_dir/web-proxy-patch.yaml" -o yaml > "$release_dir/web-restricted.yaml"
mv "$release_dir/web-restricted.yaml" "$release_dir/web.yaml"
```

### 应用清单并等待就绪

在应用前检查清单。Web 示例的副本和资源按实际容量调整。

```bash
test "$(kubectl config current-context)" = "$target_context"
kubectl apply --dry-run=server -f "$release_dir/api.yaml" -f "$release_dir/web.yaml" -f "$release_dir/web-service.yaml"
kubectl apply -f "$release_dir/api.yaml" -f "$release_dir/web.yaml" -f "$release_dir/web-service.yaml"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-web --timeout=180s
kubectl -n kube-aiops-system get pods,svc
```

### 通过本机端口转发访问

`port-forward` 模式下，在有目标集群访问权限的终端运行：

```bash
kubectl -n kube-aiops-system port-forward --address 127.0.0.1 svc/kube-aiops-web 4173:8080
```

保持该终端运行，打开 <http://127.0.0.1:4173/#/findings>。在另一终端验证页面和真实集群数据：

```bash
curl -fsS http://127.0.0.1:4173/portal-healthz
curl -fsS http://127.0.0.1:4173/api/v1/findings
curl -fsS http://127.0.0.1:4173/api/v1/findings/summary
```

若在远程安装主机运行 kubectl，可在浏览器所在机器建立 SSH 隧道：`ssh -N -L 4173:127.0.0.1:4173 user@install-host`。`production` 模式继续配置 [HTTPS 入口](#挂载-https-入口)。

## 配置可观测性来源

Kubernetes Events 通过 API 的 Kubernetes RBAC 读取。以下三个后端在 API Deployment 中按需配置。

| 来源 | 启用变量 | 选填变量 |
| --- | --- | --- |
| Prometheus | `PROMETHEUS_URL` | `PROMETHEUS_CLUSTER_LABEL_VALUE`、`PROMETHEUS_BEARER_TOKEN_FILE`、`PROMETHEUS_CA_FILE` |
| VictoriaLogs | `VICTORIALOGS_URL` | `VICTORIALOGS_CLUSTER_LABEL_VALUE`、`VICTORIALOGS_BEARER_TOKEN_FILE`、`VICTORIALOGS_CA_FILE` |
| Alertmanager | `ALERTMANAGER_URL` | `ALERTMANAGER_CLUSTER_LABEL_VALUE`、`ALERTMANAGER_BEARER_TOKEN_FILE`、`ALERTMANAGER_CA_FILE` |

共享数据源通过 `*_CLUSTER_LABEL_VALUE` 对齐本集群的 `cluster` 标签。Bearer token 通过挂载的 Secret 文件提供，携带 token 的地址使用 HTTPS。自定义 CA 指向 Pod 内可读的 PEM 文件。查询范围和需要的资源标签见 [关联设计](phase-2-correlation-design.md)。

部署配置更新后，打开一个有 Result 的 Finding，检查各来源状态和证据。需要从命令行核对时，使用列表返回的 `id` 替换下面的占位符。

```bash
curl -fsS 'http://127.0.0.1:4173/api/v1/findings/<finding-id>/correlation'
```

## 生产部署

完成企业身份接入后，从同一源码构建并发布生产镜像，再按前面的现有集群步骤部署。使用已验证的镜像 digest、发布清单和权限策略，按目标环境配置域名、证书和 Secret 引用。

### 生产身份接入

仓库默认的 `cmd/api/security.go` 只在生产组合中装配审计和脱敏。缺少可信 `Authenticator` 或 `Authorizer` 时，`SECURITY_MODE=production` 会在监听端口前退出，日志中的 `reason` 为 `security_bundle_invalid`。**企业身份接入是发布 Portal 的前置条件。** K8sGPT Operator 和 Engine 可先独立安装。

在生产构建使用的源码中，将企业身份适配器和授权策略接入 `cmd/api` 传入的 `security.Bundle`，并保留审计与脱敏。认证应校验受信任的会话或令牌。采用身份代理时，后端需验证代理身份或签名，并限制可访问 API 的网络路径。授权策略按集群、命名空间和能力分配，Portal 列表、详情和证据关联分别使用相应的读权限。浏览器会话应能通过同源 `/api/v1` 请求完成身份传递。接入与测试边界见 [身份契约](phase-1.4.1-identity-contract.md)、[授权模型](phase-1.4.2-authorization.md) 和 [生产启动门禁](phase-1.4.5-production-gates.md)。

```bash
go test ./cmd/api ./internal/security ./internal/httpapi
make api-contract-check
```

将真实身份接入的成功、拒绝和依赖异常用例加入发布测试。身份配置、审计接收方和证书由平台管理，运行配置保持 `SECURITY_MODE=production`。

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
test "$portal_access" = production
test "$(kubectl config current-context)" = "$target_context"
cp deploy/api/httproute.yaml "$release_dir/api-route.yaml"
cp deploy/web/httproute.yaml "$release_dir/web-route.yaml"
cp deploy/api/networkpolicy.yaml "$release_dir/api-networkpolicy.yaml"
cp deploy/web/networkpolicy.yaml "$release_dir/web-networkpolicy.yaml"
```

在两份 Route 中填写实际 Gateway 名称、命名空间、listener 名称和域名。集群需要支持 NetworkPolicy 的 CNI。两份策略允许指定 Envoy 代理 Pod 访问各自后端的 TCP 8080。根据实际代理 Pod 的命名空间和标签调整 `namespaceSelector` 和 `podSelector`。使用其他 Gateway 实现时填写该实现的数据面命名空间和标签。策略仅限制入站，平台已有的出站策略还需允许 DNS、Kubernetes API 和已启用的上游。

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

从端口转发模式切换到生产访问时，确认生产入口正常后，清理此前由本文档创建的 `kube-aiops-api-from-web` 和 `kube-aiops-web-restricted` 策略。NetworkPolicy 的允许规则会叠加，保留旧策略会继续允许 Web Pod 直接访问 API。

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
test "$(kubectl config current-context)" = "$target_context"
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

端口转发可用 Ctrl+C 停止。清理 Portal 时，先下线两条专用 HTTPRoute，再删除本次发布的 Deployment、Service 和 NetworkPolicy。按发布清单核对资源归属后再删除；共享 Gateway、证书和其他系统的资源由平台维护。

在已有测试集群中，`make clean-demo` 清理故障样例，`make uninstall` 清理 K8sGPT Release、实例和项目 RBAC。默认保留 Secret、Namespace 和 CRD。使用 `PURGE_SECRET=true make uninstall` 可同时删除 Provider Secret。
