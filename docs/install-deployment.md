# kube-aiops 安装与部署

| 使用场景 | 入口 |
| --- | --- |
| 本地演示 Web Portal 和证据关联 | `make portal-demo` |
| 验证 Kubernetes 组件及只读权限 | [Kind 验收命令](#3-在隔离-kind-中联调-api-和-portal) |
| 安装 K8sGPT Engine 并生成实际 Finding | [安装 K8sGPT](#2-安装-k8sgpt) |
| 在隔离 Kind 中联调 API 和 Portal | [Kind 联调](#3-在隔离-kind-中联调-api-和-portal) |
| 生产发布、验收和回滚 | [生产部署](#生产部署) |

在仓库根目录执行以下命令。版本基线以 [platform-versions.env](../config/platform-versions.env) 为准。

## 1. 依赖和预检

| 场景 | 依赖 |
| --- | --- |
| 本地 Demo | `make portal-demo` 自动准备 Go 和 Node.js（含 npm/npx）。主机预装 Bash、OpenSSL、curl 和基础命令 |
| Kubernetes 安装 | `make install` 自动补齐缺失的 kubectl v1.36.4 和 Helm v3.21.3。主机预装 Python 3、PyYAML、curl、sha256sum、tar、awk，并配置 kubeconfig |
| Kind 联调和镜像构建 | 上述 Kubernetes 工具、Kind v0.33.0、Docker |
| 完整仓库预检 | ShellCheck、kubeconform 和 [OpenAPI 校验依赖](../tools/openapi/requirements.txt) |

Linux x86_64 和 arm64 的自动安装会下载并校验官方二进制，默认存放在 `$HOME/.local/share/kube-aiops/tools`。可用 `KUBE_AIOPS_TOOLS_DIR` 指定其他可写目录。安装器无需 sudo。集群安装复用 `PATH` 上已有的 kubectl 和 Helm 3。已有集群请使用与 API Server 版本相差不超过一个次版本的 kubectl。

安装、验收、状态查询和卸载统一使用 Helm 3。检测到 Helm 4 或找不到可用的 Helm 3 时，脚本复用缓存中的 Helm 3，或安装 `helm-v3.21.3/bin/helm` 到工具目录。终端直接运行 `helm` 时仍按系统 `PATH` 选取。这个版本用于保持现有 Release 查询和 RBAC post-renderer 的调用方式。

```bash
make cluster-deps
export PATH="$PATH:${KUBE_AIOPS_TOOLS_DIR:-$HOME/.local/share/kube-aiops/tools}/bin"
kubectl config current-context
kubectl version
python3 -c 'import yaml'
make platform-check
```

`make cluster-deps` 只准备本机 CLI，不连接集群，并打印项目使用的 Helm 路径和版本。上面的 `export` 供当前 Shell 直接运行 kubectl。`make bootstrap`、`make install`、`make verify` 等项目命令可直接找到项目安装的 CLI。

执行安装的 Kubernetes 身份需要管理项目 CRD、Namespace、Secret、Lease 和 RBAC，并可模拟项目 ServiceAccount 执行授权检查。节点需要能够拉取 Operator 和 Engine 镜像。Engine 需要访问配置的 AI Provider。

本地只读 Demo 启动时会检测 Go、Node.js、npm 和 npx。脚本复用可用的 Go 1.26.0 及以上和 Node.js 22 及以上。工具链缺失或不兼容时，自动安装 Go 1.26.5 或 Node.js 22.19.0。可运行 `make portal-deps` 单独准备工具链。首次构建前端还需要 npm registry 或已有的 esbuild 缓存。

```bash
make portal-demo
```

默认打开 <http://127.0.0.1:4173/#/findings> 查看简体中文诊断控制台。在远程测试主机上运行 Demo 时，可通过 `PORTAL_DEMO_HOST` 和 `PORTAL_DEMO_PORT` 覆盖监听地址和端口。

```bash
PORTAL_DEMO_HOST=0.0.0.0 PORTAL_DEMO_PORT=4173 make portal-demo
```

在浏览器访问 `http://<服务器IP>:4173/#/findings`。Go API 和 HTTPS 样例服务监听回环地址，由 Portal 转发请求。运行 `make portal-demo-check` 可自动验收并清理。演示数据与操作步骤见 [Portal Demo](portal-demo.md)。

## 2. 安装 K8sGPT

以下命令使用当前 kubectl context。先核对目标集群，再创建 Provider Secret。默认使用 OpenAI、`gpt-4o-mini` 和 5 分钟分析周期。使用其他 OpenAI 兼容端点时，先参考 [配置 Engine 模型与语言](#配置-engine-模型与语言)。

```bash
kubectl config current-context
read -rsp 'OpenAI token: ' OPENAI_TOKEN
printf '\n'
export OPENAI_TOKEN
make bootstrap-secret
unset OPENAI_TOKEN

make install
make verify
```

`make bootstrap-secret` 创建 `k8sgpt-operator-system/k8sgpt-openai-secret`。安装器部署 Operator v0.2.29、Engine v0.4.37 和固定的只读 RBAC，验证 Operator 和 Engine 的 rollout 状态。

### 使用内网镜像仓库

将下面三张镜像同步到节点可访问的镜像仓库，并保留各自的版本标签。`K8SGPT_IMAGE_REGISTRY` 指定 `仓库地址[:端口][/项目路径]`，安装器以 `仓库/镜像:版本` 拉取。

| 镜像 | 默认仓库 | 使用 `harbor.example.com/public` 时 | 单独覆盖的变量 |
| --- | --- | --- | --- |
| Operator | `ghcr.io/k8sgpt-ai/k8sgpt-operator` | `harbor.example.com/public/k8sgpt-operator:v0.2.29` | `K8SGPT_OPERATOR_IMAGE_REPOSITORY` |
| RBAC Proxy | `quay.io/brancz/kube-rbac-proxy` | `harbor.example.com/public/kube-rbac-proxy:v0.19.1` | `K8SGPT_RBAC_PROXY_IMAGE_REPOSITORY` |
| Engine | `ghcr.io/k8sgpt-ai/k8sgpt` | `harbor.example.com/public/k8sgpt:v0.4.37` | `K8SGPT_ENGINE_IMAGE_REPOSITORY` |

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
  docker://ghcr.io/k8sgpt-ai/k8sgpt@sha256:ebf0d1f5a8463190abdf1a9c84282cfbd9ce611e3dc1c490194b7aaf2676d088 \
  "docker://${mirror}/k8sgpt:v0.4.37"
```

在安装主机上执行：

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

在仓库根目录更新 `main`，核对输出的 context 对应目标集群。

```bash
git switch main && git pull --ff-only origin main && kubectl config current-context
```

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

K8SGPT_IMAGE_REGISTRY=harbor.example.com/public \
K8SGPT_OPERATOR_MEMORY_LIMIT=1Gi \
make install && make verify
```

Engine 继续使用 `openai` backend，需能访问配置的端点。仓库的 Engine 出站策略允许 DNS 以及 TCP 443、6443；使用其他端口时需按实际 Provider 配置出站策略。升级时复用这些变量。`make verify` 会核对当前会话中指定的模型、Base URL 和语言是否已写入 CR，实际调用与结果生成在目标集群中验收。

```bash
kubectl -n k8sgpt-operator-system get k8sgpt k8sgpt-engine \
  -o jsonpath='{.spec.ai.backend}{"\t"}{.spec.ai.model}{"\t"}{.spec.ai.baseUrl}{"\t"}{.spec.ai.language}{"\n"}'
```

等待一次分析周期后，检查分析状态与诊断结果。

```bash
REQUIRE_ANALYSIS_HEALTH=true make verify
make results
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

## 3. 在隔离 Kind 中联调 API 和 Portal

自动验收的三个入口分别覆盖集群生命周期、只读 API 和 Events 证据。各脚本创建和清理自己的测试集群。

```bash
make e2e
bash tests/e2e-api-kind.sh
bash tests/e2e-correlation-events-kind.sh
```

手动联调需要一个独立的 Kind 集群，并在该集群完成上一节的 K8sGPT 安装和 Result 验证。

### 创建隔离集群

在独立的 Bash 会话中运行下面的命令。专用 kubeconfig 会保存在临时目录。

```bash
source config/platform-versions.env
cluster=kube-aiops-portal-demo
portal_kube_dir="$(mktemp -d)"
export KUBECONFIG="$portal_kube_dir/config"

kind create cluster --name "$cluster" --image "$KIND_NODE_IMAGE" --wait 120s
kubectl config current-context
```

### 部署 Go API

在同一个会话中安装 K8sGPT 并确认有 Result，然后构建并导入 API 和 Web 镜像。以下镜像导入方式对应仓库单节点 Kind 测试。

```bash
docker build -t kube-aiops-api:dev .
docker build -f web/Dockerfile -t kube-aiops-web:demo .
docker save kube-aiops-api:dev kube-aiops-web:demo |
  docker exec -i "${cluster}-control-plane" ctr --namespace=k8s.io images import -

kubectl apply -f deploy/api/namespace.yaml
kubectl apply -f deploy/api/serviceaccount.yaml \
  -f deploy/api/clusterrole.yaml -f deploy/api/clusterrolebinding.yaml \
  -f deploy/api/service.yaml
kubectl set env --local -f deploy/api/deployment.yaml SECURITY_MODE=development -o yaml |
  kubectl apply -f -
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=120s
```

`development` 是隔离测试的显式模式。后端通过 Pod 挂载的 ServiceAccount token 和 CA 访问 Kubernetes API，默认读取 `k8sgpt-operator-system` 中属于 `k8sgpt-engine` 的 Result。

### 部署 Web Portal

生成本次联调用的 Nginx 配置。浏览器和 API 使用同一入口，`/api/v1/` 由 Nginx 转发到集群内的 API Service。

```bash
awk '
  $0 == "    location / {" {
    print "    location /api/v1/ {"
    print "      proxy_pass http://kube-aiops-api.kube-aiops-system.svc:8080;"
    print "    }"
  }
  { print }
' web/nginx.conf > "$portal_kube_dir/nginx.conf"

kubectl -n kube-aiops-system create configmap kube-aiops-web-demo \
  --from-file=nginx.conf="$portal_kube_dir/nginx.conf" --dry-run=client -o yaml |
  kubectl apply -f -

kubectl apply -f - <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-aiops-web
  namespace: kube-aiops-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kube-aiops-web
  template:
    metadata:
      labels:
        app: kube-aiops-web
    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 101
        runAsGroup: 101
        fsGroup: 101
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: web
          image: kube-aiops-web:demo
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 8080
          readinessProbe:
            httpGet:
              path: /portal-healthz
              port: http
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
          volumeMounts:
            - name: nginx-config
              mountPath: /etc/nginx/nginx.conf
              subPath: nginx.conf
              readOnly: true
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: nginx-config
          configMap:
            name: kube-aiops-web-demo
        - name: tmp
          emptyDir: {}
YAML
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-web --timeout=120s
kubectl -n kube-aiops-system port-forward --address 127.0.0.1 deployment/kube-aiops-web 4173:8080
```

在浏览器打开 <http://127.0.0.1:4173/#/findings>。保持 port-forward 运行，在另一个终端验证页面和同源 API。

```bash
curl -fsS http://127.0.0.1:4173/portal-healthz
curl -fsS http://127.0.0.1:4173/api/v1/findings
curl -fsS http://127.0.0.1:4173/api/v1/findings/summary
```

## 4. 配置可观测性来源

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

### 1. 预检目标集群和发布输入

先在预发集群完成整套流程，再将同一组已验证的镜像 digest 和清单模板推广到生产，使用生产环境对应的域名、身份策略和 Secret 引用。安装端需要 Bash、kubectl、Helm 3、Python 3 和 PyYAML。构建端需要 Docker Buildx 和仓库推送权限，命令行验收需要 curl 和 jq。依赖准备见 [依赖和预检](#1-依赖和预检)。

| 输入 | 发布前确认 |
| --- | --- |
| Kubernetes | 目标 context、待部署节点架构，以及安装身份的 Namespace、RBAC、Lease、Secret 和工作负载管理权限 |
| 镜像仓库 | 节点能够拉取 K8sGPT、API、Web 镜像。需要认证时，确认各命名空间的拉取凭据 |
| K8sGPT | 已批准的 AI Provider、模型和 Secret。默认 CR 使用 OpenAI，Engine 的出站访问和凭据需对应实际 Provider |
| Portal API | 已接入企业认证与授权的 API 源码和预发验收结果 |
| HTTPS 入口 | 已有 Envoy Gateway、已配置 TLS 证书的 HTTPS listener、DNS 和允许挂载的命名空间 |
| 回滚材料 | 上一版清单、API/Web 镜像 digest、身份策略版本、K8sGPT 安装参数和 Helm revision |

在同一个 Bash 会话中设置变量。预发和生产分别使用各自的 context、入口和域名。

```bash
set -euo pipefail
make cluster-deps
export PATH="$PATH:${KUBE_AIOPS_TOOLS_DIR:-$HOME/.local/share/kube-aiops/tools}/bin"

target_context=production
registry=harbor.example.com/kube-aiops
mirror=harbor.example.com/public
portal_host=kube-aiops.example.com
gateway_namespace=envoy-gateway-system
gateway_name=production-gateway
gateway_listener=https
platforms=linux/amd64
revision="$(git rev-parse --short=12 HEAD)"
release_dir="$PWD/.build/production/$target_context/$revision"

test "$(kubectl config current-context)" = "$target_context"
kubectl version
kubectl get nodes -L kubernetes.io/arch
make platform-check
```

混合架构集群可将 `platforms` 设置为 `linux/amd64,linux/arm64`，并确认构建器支持对应平台。

### 2. 接入 Portal API 的生产身份

仓库默认的 `cmd/api/security.go` 只在生产组合中装配审计和脱敏。缺少可信 `Authenticator` 或 `Authorizer` 时，`SECURITY_MODE=production` 会在监听端口前退出，日志中的 `reason` 为 `security_bundle_invalid`。**企业身份接入是发布 Portal 的前置条件。** K8sGPT Operator 和 Engine 可先独立安装。

在生产构建使用的源码中，将企业身份适配器和授权策略接入 `cmd/api` 传入的 `security.Bundle`，并保留审计与脱敏。认证应校验受信任的会话或令牌。采用身份代理时，后端需验证代理身份或签名，并限制可访问 API 的网络路径。授权策略按集群、命名空间和能力分配，Portal 列表、详情和证据关联分别使用相应的读权限。浏览器会话应能通过同源 `/api/v1` 请求完成身份传递。接入与测试边界见 [身份契约](phase-1.4.1-identity-contract.md)、[授权模型](phase-1.4.2-authorization.md) 和 [生产启动门禁](phase-1.4.5-production-gates.md)。

```bash
go test ./cmd/api ./internal/security ./internal/httpapi
make api-contract-check
```

将真实身份接入的成功、拒绝和依赖异常用例加入发布测试。身份配置、审计接收方和证书由平台管理，运行配置保持 `SECURITY_MODE=production`。

### 3. 发布 API 和 Web 镜像

在已提交身份集成的源码版本构建。Buildx 将实际发布的 digest 写入元数据，后续 Deployment 使用该 digest。以下目录保存本次发布材料，预发验收后应归档到发布系统。生产推广时检出同一源码版本，将两个 Buildx 元数据文件恢复到当前环境的 `release_dir`，直接执行下一段 digest 读取命令。

```bash
mkdir -p "$release_dir"
docker login "${registry%%/*}"

docker buildx build --platform "$platforms" --push \
  --metadata-file "$release_dir/api-build.json" -t "$registry/api:$revision" .
docker buildx build --platform "$platforms" --push -f web/Dockerfile \
  --metadata-file "$release_dir/web-build.json" -t "$registry/web:$revision" .
```

```bash
api_digest="$(jq -er '."containerimage.digest" | select(test("^sha256:[0-9a-f]{64}$"))' "$release_dir/api-build.json")"
web_digest="$(jq -er '."containerimage.digest" | select(test("^sha256:[0-9a-f]{64}$"))' "$release_dir/web-build.json")"
api_image="$registry/api@$api_digest"
web_image="$registry/web@$web_digest"
printf '%s\n' "$api_image" "$web_image"
```

### 4. 安装并验收 K8sGPT

按 [使用内网镜像仓库](#使用内网镜像仓库) 同步 Operator、RBAC Proxy 和 Engine 的版本标签。下例使用内网仓库，`1Gi` 为 Operator `manager` 内存上限的示例，生产值按实际负载确定。需要自定义模型或语言时，先设置 [Engine AI 变量](#配置-engine-模型与语言)，并确认 Secret 对应目标 Provider。升级时继续传入已批准的镜像和资源参数，详见 [调整 Operator 资源](#调整-operator-资源)。

```bash
test "$(kubectl config current-context)" = "$target_context"
make bootstrap

provider_secret="$(kubectl -n k8sgpt-operator-system get secret k8sgpt-openai-secret --ignore-not-found -o name)"
if [[ -z "$provider_secret" ]]; then
  read -rsp 'OpenAI token: ' OPENAI_TOKEN
  printf '\n'
  (export OPENAI_TOKEN; make bootstrap-secret)
  unset OPENAI_TOKEN
fi

K8SGPT_IMAGE_REGISTRY="$mirror" K8SGPT_OPERATOR_MEMORY_LIMIT=1Gi make install
make verify
make status
make results
```

使用 `make results` 核对已有诊断结果，或在预发用既有测试样例完成结果验收。

### 5. 部署只读 API 与 Web Portal

应用仓库提供的 API Namespace、ServiceAccount、只读 RBAC 和 ClusterIP Service。通过本地渲染将 API Deployment 的镜像替换为已验收的 digest。

```bash
test "$(kubectl config current-context)" = "$target_context"
kubectl apply -f deploy/api/namespace.yaml
kubectl apply -f deploy/api/serviceaccount.yaml -f deploy/api/clusterrole.yaml \
  -f deploy/api/clusterrolebinding.yaml -f deploy/api/service.yaml
kubectl set image --local -f deploy/api/deployment.yaml "api=$api_image" -o yaml \
  > "$release_dir/api.yaml"

cat > "$release_dir/web.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-aiops-web
  namespace: kube-aiops-system
spec:
  replicas: 2
  selector:
    matchLabels:
      app.kubernetes.io/name: kube-aiops-web
  template:
    metadata:
      labels:
        app.kubernetes.io/name: kube-aiops-web
    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 101
        runAsGroup: 101
        fsGroup: 101
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: web
          image: $web_image
          ports:
            - name: http
              containerPort: 8080
          readinessProbe:
            httpGet:
              path: /portal-healthz
              port: http
          livenessProbe:
            httpGet:
              path: /portal-healthz
              port: http
          resources:
            requests:
              cpu: 20m
              memory: 32Mi
            limits:
              cpu: 250m
              memory: 128Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop:
                - ALL
          volumeMounts:
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: tmp
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: kube-aiops-web
  namespace: kube-aiops-system
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: kube-aiops-web
  ports:
    - name: http
      port: 8080
      targetPort: http
YAML
```

私有镜像仓库使用 `kube-aiops-system` 中预建的 `kubernetes.io/dockerconfigjson` Secret。在相应 Deployment 的 `spec.template.spec` 下合并以下字段，使用实际 Secret 名称。

```yaml
imagePullSecrets:
  - name: harbor-pull
```

将身份适配器所需的配置和 Secret 引用写入 `api.yaml`。按需接入 [Prometheus、VictoriaLogs 和 Alertmanager](#4-配置可观测性来源)。例如使用预建 Secret `kube-aiops-prometheus` 中的 `token` 和 `ca.crt`，可向 API Pod 模板合并以下字段，并将 URL 替换为实际 HTTPS 地址。

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

在应用前检查清单。Web 示例的副本和资源按实际容量调整。

```bash
kubectl apply --dry-run=server -f "$release_dir/api.yaml" -f "$release_dir/web.yaml"
kubectl apply -f "$release_dir/api.yaml" -f "$release_dir/web.yaml"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-web --timeout=180s
kubectl -n kube-aiops-system get pods,svc
```

### 6. 挂载 HTTPS 入口

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

按环境将 `portal_host` 解析到 Gateway 地址。下面的 `HTTPRoute` 保留原始路径，将 `/api/v1` 路由到 API，其余请求路由到 Web。

```bash
kubectl -n "$gateway_namespace" wait --for=condition=Programmed "gateway/$gateway_name" --timeout=120s
kubectl -n "$gateway_namespace" get gateway "$gateway_name" -o jsonpath='{.status.addresses[*].value}{"\n"}'
cat > "$release_dir/route.yaml" <<YAML
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: kube-aiops
  namespace: kube-aiops-system
spec:
  parentRefs:
    - name: $gateway_name
      namespace: $gateway_namespace
      sectionName: $gateway_listener
  hostnames:
    - $portal_host
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api/v1
      backendRefs:
        - name: kube-aiops-api
          port: 8080
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: kube-aiops-web
          port: 8080
YAML

kubectl apply --dry-run=server -f "$release_dir/route.yaml"
kubectl apply -f "$release_dir/route.yaml"
kubectl -n kube-aiops-system get httproute kube-aiops -o yaml
```

核对目标父级下的 `Accepted=True`、`ResolvedRefs=True`，以及状态所对应的当前 generation。若使用预发域名验证，在完成下节后再切生产 DNS 或流量。

### 7. 验收、发布与回滚

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

生产沿用预发已验证的清单和 digest。保留本次以及上一版的 `api.yaml`、`web.yaml`、`route.yaml`、镜像 digest 和身份策略版本。出现故障时，恢复上一版相互兼容的身份策略与发布清单，再复验。

```bash
test "$(kubectl config current-context)" = "$target_context"
previous=/path/to/previous-approved-release
kubectl apply --dry-run=server -f "$previous/api.yaml" -f "$previous/web.yaml" -f "$previous/route.yaml"
kubectl apply -f "$previous/api.yaml" -f "$previous/web.yaml" -f "$previous/route.yaml"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-web --timeout=180s
make verify
```

首次发布且尚无上一版时，撤销本次新增的流量入口并保留工作负载供排查。K8sGPT 安装过程中的失败由安装器按 Lease 和快照处理回滚。若安装已成功但需回退配置，使用上一版获批的源码和参数重新执行 `make install`，然后运行 `make verify`。安装器报告回滚失败时，先按日志给出的快照和恢复报告检查 Helm、RBAC 与 K8sGPT CR 状态。

## 6. 排查与清理

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

本地 Demo 用 Ctrl+C 停止。隔离 Kind 联调结束后，先停止 port-forward，再删除本次创建的集群和临时 kubeconfig。

```bash
kind delete cluster --name "$cluster"
rm -r -- "$portal_kube_dir"
unset KUBECONFIG
```

在已有测试集群中，`make clean-demo` 清理故障样例，`make uninstall` 清理 K8sGPT Release、实例和项目 RBAC。默认保留 Secret、Namespace 和 CRD。使用 `PURGE_SECRET=true make uninstall` 可同时删除 Provider Secret。
