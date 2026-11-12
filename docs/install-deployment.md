# kube-aiops 安装与部署

| 使用场景 | 入口 |
| --- | --- |
| 本地演示 Web Portal 和证据关联 | `make portal-demo` |
| 验证 Kubernetes 组件及只读权限 | [Kind 验收命令](#3-在隔离-kind-中联调-api-和-portal) |
| 安装 K8sGPT Engine 并生成实际 Finding | [安装 K8sGPT](#2-安装-k8sgpt) |
| 在隔离 Kind 中联调 API 和 Portal | [Kind 联调](#3-在隔离-kind-中联调-api-和-portal) |
| 准备生产镜像和同源路由 | [生产部署条件](#5-生产部署条件) |

在仓库根目录执行以下命令。版本基线以 [platform-versions.env](../config/platform-versions.env) 为准。

## 1. 依赖和预检

| 场景 | 依赖 |
| --- | --- |
| 本地 Demo | `make portal-demo` 自动准备 Go 和 Node.js（含 npm/npx）。主机预装 Bash、OpenSSL、curl 和基础命令 |
| Kubernetes 安装 | `make install` 自动补齐缺失的 kubectl v1.36.4 和 Helm v3.21.3。主机预装 Python 3、PyYAML、curl、sha256sum、tar、awk，并配置 kubeconfig |
| Kind 联调和镜像构建 | 上述 Kubernetes 工具、Kind v0.33.0、Docker |
| 完整仓库预检 | ShellCheck、kubeconform 和 [OpenAPI 校验依赖](../tools/openapi/requirements.txt) |

Linux x86_64 和 arm64 的自动安装会下载并校验官方二进制，默认存放在 `$HOME/.local/share/kube-aiops/tools`。可用 `KUBE_AIOPS_TOOLS_DIR` 指定其他可写目录。安装器无需 sudo。集群安装复用 `PATH` 上已有的 kubectl 和 Helm，已有集群请使用与 API Server 版本相差不超过一个次版本的 kubectl。

```bash
make cluster-deps
export PATH="$PATH:${KUBE_AIOPS_TOOLS_DIR:-$HOME/.local/share/kube-aiops/tools}/bin"
kubectl config current-context
kubectl version
helm version --short
python3 -c 'import yaml'
make platform-check
```

`make cluster-deps` 只准备本机 CLI，不连接集群。上面的 `export` 供当前 Shell 直接运行客户端。`make bootstrap`、`make install`、`make verify` 等项目命令可直接找到项目安装的 CLI。

执行安装的 Kubernetes 身份需要管理项目 CRD、Namespace、Secret、Lease 和 RBAC，并可模拟项目 ServiceAccount 执行授权检查。节点需要能够拉取 Operator 和 Engine 镜像。Engine 需要访问配置的 AI Provider。

本地只读 Demo 启动时会检测 Go、Node.js、npm 和 npx。脚本复用可用的 Go 1.26.0 及以上和 Node.js 22 及以上。工具链缺失或不兼容时，自动安装 Go 1.26.5 或 Node.js 22.19.0。可运行 `make portal-deps` 单独准备工具链。首次构建前端还需要 npm registry 或已有的 esbuild 缓存。

```bash
make portal-demo
```

默认打开 <http://127.0.0.1:4173/#/findings>。在远程测试主机上运行 Demo 时，可通过 `PORTAL_DEMO_HOST` 和 `PORTAL_DEMO_PORT` 覆盖监听地址和端口。

```bash
PORTAL_DEMO_HOST=0.0.0.0 PORTAL_DEMO_PORT=4173 make portal-demo
```

在浏览器访问 `http://<服务器IP>:4173/#/findings`。Go API 和 HTTPS 样例服务监听回环地址，由 Portal 转发请求。运行 `make portal-demo-check` 可自动验收并清理。演示数据与操作步骤见 [Portal Demo](portal-demo.md)。

## 2. 安装 K8sGPT

以下命令使用当前 kubectl context。先核对目标集群，再创建 Provider Secret。仓库默认的 `K8sGPT` CR 使用 OpenAI、`gpt-4o-mini` 和 5 分钟分析周期。Provider 需要与部署的 CR 匹配。

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

## 5. 生产部署条件

API 的默认 Deployment 使用 `SECURITY_MODE=production`。当前默认 API 入口装配了审计和响应脱敏，生产部署前还需在安全组合中接入可信 Authenticator 和 Authorizer。缺少这些组件时，进程会在启动阶段终止。接入后验证受保护路由的 401、403、资源范围授权和审计记录，相关约束见 [Phase 1.4 安全验收](phase-1.4-completion.md)。

安全组合接入后，构建和发布 API、Web 镜像。将 `registry` 替换为已登录的镜像仓库。

```bash
registry=registry.example.com/kube-aiops
revision="$(git rev-parse --short=12 HEAD)"
docker build -t "$registry/api:$revision" .
docker build -f web/Dockerfile -t "$registry/web:$revision" .
docker push "$registry/api:$revision"
docker push "$registry/web:$revision"
```

Deployment 以镜像仓库返回的 digest 固定版本。TLS 入口将同一域名下的 `/api/v1/` 转发给 API Service，其余 Portal 路径转发给 Web 服务。生产 API 使用项目定义的只读 ServiceAccount 和 RBAC。在预发环境验证路由、身份、Result 读取和四类证据状态后发布。

## 6. 排查与清理

| 现象 | 核对项 |
| --- | --- |
| 下载依赖失败 | 主机能否通过 HTTPS 访问 `go.dev`、`nodejs.org`、`dl.k8s.io`、`get.helm.sh` 及下载重定向地址。也可提前安装工具并加入 `PATH` |
| 安装提示缺少 Secret | 当前 context、目标 Namespace 以及 `make bootstrap-secret` 的输出 |
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
