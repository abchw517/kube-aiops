# kube-aiops 安装与部署

在已有 Kubernetes 集群中按顺序完成 [预检](#1-依赖和预检) → [安装 K8sGPT](#2-安装-k8sgpt) → [准备镜像](#3-准备-api-和-web-镜像) → [部署 API 与 Web Portal](#4-部署-api-与-web-portal)。已有 K8sGPT 部署时，先核对其 Namespace、实例名和 Result。API 清单默认读取 `k8sgpt-operator-system/k8sgpt-engine`，使用其他实例时，按实际配置调整 API 清单中的 `K8SGPT_NAMESPACE` 和 `K8SGPT_NAME`，并核对 ServiceAccount 的读取权限。

部署基础服务后，可按需 [接入多个集群](#在控制台接入-kubernetes-集群)、[配置数据源](#配置可观测性来源) 和 [部署 HolmesGPT](#部署-holmesgpt)。API 和 Web 的 Docker 构建均以仓库根目录为上下文。

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

继续 [生产身份接入](#生产身份接入)，完成 SQLite、API 清单和 HTTPS 入口配置。随后从控制台[配置数据源](#配置可观测性来源)。

#### 检查本地节点采集权限

部署或升级 API 后，用有权读取 Deployment 并模拟其 ServiceAccount 的账号核对集群级节点读取权限。默认检查 `kube-aiops-system/kube-aiops-api`，自定义部署可传入 `--namespace` 和 `--deployment`。

```bash
kube_context="$(kubectl config current-context)"
python3 scripts/check-api-node-rbac.py --context "$kube_context"
```

若返回缺少权限，确认目标上下文及服务账号后，由有权管理集群 RBAC 的管理员对同一命令追加 `--repair`。脚本仅向该 API 的专用 ServiceAccount 授予集群级 `list nodes`，并重新检查生效结果。在「资源管理 → Kubernetes 集群管理」对本地自动采集实例点「重新校验」，确认节点列表恢复。

### 应用清单并等待就绪

在应用前检查清单。Web 示例的副本和资源按实际容量调整。

```bash
kubectl apply --dry-run=server -f "$release_dir/api.yaml" -f "$release_dir/web.yaml" -f "$release_dir/web-service.yaml"
kubectl apply -f "$release_dir/api.yaml" -f "$release_dir/web.yaml" -f "$release_dir/web-service.yaml"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-web --timeout=180s
kubectl -n kube-aiops-system get pods,svc
```

## 生产部署

准备同一源码版本的 API 与 Web 镜像及已验收的 digest。依次配置 SQLite、应用工作负载、发布同源 HTTPS 入口并初始化管理员。LDAP 按需在首次登录后配置。

### 生产身份接入

以下步骤适用于全新部署。SQLite 在首次启动时初始化，首次登录后通过 Portal 管理账号、权限和 LDAP 配置。按顺序完成存储和清单配置、首次改密、本地账号授权，再按需启用 LDAP。

#### 1. 创建 SQLite PVC

API 和 Web 镜像需包含 SQLite 与系统管理功能。先完成 [部署 API 与 Web Portal](#4-部署-api-与-web-portal) 中的镜像渲染，准备本次发布的 `api.yaml`、`web.yaml` 和 `web-service.yaml`，暂缓应用 Deployment。

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

API 以 UID/GID 65532 运行，PVC 挂载目录需要可写。单副本配合 `Recreate` 使用同一个 SQLite 卷。默认会话有效期为 30 分钟，可在系统管理调整。API 重启后需重新登录。

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

#### 4. 验证本地角色并配置接口权限

1. 进入 **系统管理 → 用户管理**，创建一个本地访客，使用至少 12 字节的临时密码。以访客登录并改密，确认可只读访问研发和测试范围。
2. 要为 LDAP 员工配置细粒度权限，先接入 LDAP 并让员工登录一次，再进入 **系统管理 → 权限管理 → 角色配置 → 新建角色**。勾选查询诊断列表、详情及所需证据等动作，或者批量勾选当前筛选结果。
3. 选择数据范围，点击 **添加到授权清单 → 保存角色 → 确认保存**。研发及测试可使用环境级范围，预发布及生产的命名空间动作需指定实际实例、业务线和命名空间。
4. 在 **用户管理** 向测试员工授予该角色，或将角色关联到用户组后添加成员。从独立会话验证授权范围与只读动作生效，以及未经授予的接口和范围被拒绝。授权来源可在账号详情核对。

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

在独立浏览器会话勾选 LDAP 登录，用测试员工完成首次登录。随后由管理员在「用户管理」找到该账号，按第 4 步直接授予角色，或将员工加入关联了所需角色的平台用户组。再用未获相关授权的账号确认诊断访问受限。LDAP 密码由目录服务验证，平台保存员工身份和组信息。

##### 验收管理员组继承

1. 用超级管理员进入 **系统管理 → 权限管理 → 用户组 → 新建用户组**，勾选 **系统管理员**，核对成员影响后确认保存。
2. 在用户管理中将测试 LDAP 员工加入该组。员工重新登录后，在账号详情核对「用户组 → 系统管理员」来源，并验证系统管理可用。
3. 从组上取消关联后再次核对影响预览。仅通过该组获得管理员角色的员工需要重新登录，有其他来源的成员继续保留管理员权限。
4. 以普通系统管理员登录，核对管理员组可查看，角色和成员关联的写入口由超级管理员控制。保存结果可定位到操作审计。

升级时同步发布本次 API 与 Web 镜像并保留现有身份 SQLite 挂载。已有账号、组和直授权限随原数据保留。新增管理员组授权由超级管理员明确提交。


#### 6. 核对部署结果

```bash
kubectl -n kube-aiops-system get deployment kube-aiops-api kube-aiops-web
kubectl -n kube-aiops-system get pvc kube-aiops-identity
kubectl -n kube-aiops-system logs deployment/kube-aiops-api --tail=100
```

API 和 Web 应各自就绪，PVC 为 `Bound`。若 API 在启动时退出，核对 `AUTH_ORIGIN`、PVC 写权限和引导配置。若 LDAP 登录返回服务不可用，核对域控连通性、证书链、搜索账号凭据和过滤器。完成验收后按发布流程开放入口，并将 SQLite 与凭据加密密钥纳入同一备份和恢复计划。

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

核对两条 Route 在目标 listener 下的 `Accepted=True`、`ResolvedRefs=True`，以及状态所对应的当前 generation。若使用预发域名，在完成验收后再切生产 DNS 或流量。

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

生产沿用预发已验证的清单和 digest。保留本次以及上一版的 `api.yaml`、`web.yaml`、`web-service.yaml`、两份 Route、两份 NetworkPolicy、镜像 digest 和身份策略版本。出现故障时，恢复上一版相互兼容的身份策略与发布清单，再复验。

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

## 在控制台接入 Kubernetes 集群

以系统管理员身份打开 **资源管理 → Kubernetes 集群管理 → 接入集群**。本地集群由平台自动发现，直接在列表中查看或编辑归属。远端集群从同一个入口选择接入方式。

1. **外部直连**适用于中心 API 能访问目标集群 Kubernetes API 的情况。在「基本信息」填写实例 ID、名称、环境和业务归属。在「连接与凭据」提交目标集群的专用只读 kubeconfig，或填写 API 地址、CA 和 Token／客户端证书。在「观测与诊断」填写目标 K8sGPT Engine 的命名空间和名称，点击「测试连接」，通过后点击「保存配置」。直连凭据由平台加密保存。
2. **采集上报**适用于目标集群主动向中心上报的情况。在控制台登记并启用实例，保存后记录仅显示一次的上报凭据。为目标集群采集端配置该凭据，并按[采集 CronJob 示例](../deploy/collector/cronjob.yaml)填写 Portal HTTPS 地址和实例 ID。首次收到快照后，在集群列表查看上报状态。
3. 在 **系统管理 → 权限管理** 按目标实例和命名空间授权，再用值班账号检查「诊断总览」中的集群筛选和诊断数据。

详细表单和凭据要求见[资源管理 · Kubernetes 集群管理](resource-management/04-development-tasks.md#kubernetes-集群管理当前实现)。

## 启用诊断详情的 AI 分析

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

使用既有模型路径时，Portal 会读取 Engine 的模型、Base URL 和其引用 Secret 的指定键。轮换同一个 Secret 的值后，下次使用该路径即可读取新密钥。命中 Holmes 绑定时，Holmes 按其自身模型配置和凭据调用。

管理员在诊断详情展开 **AI 复核**，查看模型对关联证据的判断、待验证项和下一步。可以补充现场观察后重新复核。API 在当前诊断的集群、命名空间、资源和时间窗口内查询关联证据，例如 Kubernetes 事件以及已配置的 Prometheus、VictoriaLogs 和 Alertmanager。请勿提交凭据或个人敏感信息。

核对并编辑复核结论后，点击 **保存复核结论**。需沉淀知识时，从诊断详情创建故障案例草稿，再进入运维知识库按治理流程发布。若结论已被其他人更新，请先加载最新版本。

诊断 AI 复核（包括补充观察后的重新复核）的模型请求默认等待 90 秒。API 容器环境变量 `FINDING_AI_PROVIDER_TIMEOUT` 支持 `5s` 到 `120s`，本地和手动接入实例共用该配置。升级前已显式配置旧值的环境需同步调整该值才能使用较长的等待时间。复核总时限为 135 秒，关联证据查询最多占 5 秒。启用诊断复核时，API 响应写入时限为 140 秒。知识库问答继续使用自身 30 秒总时限。同时启用 Holmes 时，回退模型和 Holmes 请求共用复核总预算。

发布时同步更新 API、Web 和 [API HTTPRoute](../deploy/api/httproute.yaml)。该清单仅为 `/api/v1/admin/finding-analysis` 设置 150 秒请求时限和 145 秒后端时限。域名前有 NGINX 时，将同一路径的 `proxy_read_timeout` 设为至少 `150s`。如果仍超时，检查 API 日志 `AI provider request failed` 中的 `elapsed_ms` 和 `upstream_status`，分别核对 API 等待时间与上游实际返回的超时状态。

诊断分析返回 JSON `504 AI_PROVIDER_TIMEOUT` 时，现场观察内容保留，可直接重新复核。持续超时时查看 API 日志中 `AI provider request failed` 的 `elapsed_ms`、`client_timeout_ms` 和 `upstream_status`。上游状态为 408 或 504 时，排查模型服务或它的上游网关。响应中缺少此错误码时，对照 Portal 入口代理日志和 API 请求日志判断等待发生在哪一层。

手动保存复核结论后，Portal 展示 SQLite 中的最新结论。原始 K8sGPT 诊断和已有复核版本可在弹窗中查看，弹窗展示最近五版。具有该诊断读取权限的用户可查看最新结论。


## 配置可观测性来源

新增连接统一从 **资源管理 → 数据源管理 → 新增数据源** 操作。先在 API 发布清单中批准服务地址，再在 Portal 填写凭据、配置范围和验证连接。Kubernetes Events 通过本地或外部直连集群的 Kubernetes API 及 RBAC 读取。

### 批准数据源地址

在 API Deployment 的容器 `env` 中加入需要使用的条目，并换成实际 HTTPS 基地址。保留反向代理路径前缀。同类型多个地址可用逗号分隔。Zabbix 批准实际 JSON-RPC 地址，表单可填写站点基地址，平台会规范化路径。

```yaml
- name: DIAGNOSTIC_ZABBIX_ENDPOINTS
  value: https://zabbix.example.com/api_jsonrpc.php
- name: DIAGNOSTIC_PROMETHEUS_ENDPOINTS
  value: https://monitor.example.com/prometheus
- name: DIAGNOSTIC_VICTORIALOGS_ENDPOINTS
  value: https://logs.example.com
- name: DIAGNOSTIC_ALERTMANAGER_ENDPOINTS
  value: https://monitor.example.com/alertmanager
- name: DIAGNOSTIC_GRAFANA_ENDPOINTS
  value: https://grafana.example.com
```

API 启动时读取地址清单。更新后应用本次 API 发布清单并等待就绪。确认 API Pod 能访问上游，且信任 HTTPS 证书。

```bash
kubectl apply --dry-run=server -f "$release_dir/api.yaml"
kubectl apply -f "$release_dir/api.yaml"
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
```

### 在控制台接入并验证

| 来源 | 配置与验收 |
| --- | --- |
| Zabbix | 填写名称、允许的地址和只读 Token 或账号密码。点击「测试连接」，选择环境、业务和主机组，预览问题并「保存并启用」。后台同步后在诊断总览检查告警。 |
| Prometheus | 选择环境、业务和实例。共享端点还要填写上游标签及精确值。测试连接、按页面提示预览范围并保存，进入有指标证据的诊断详情验收。 |
| VictoriaLogs | 选择环境、业务和实例。共享端点填写字段和精确值，按上游配置填写租户 AccountID、ProjectID。通过连接和范围验证后，在诊断详情检查日志证据。 |
| Alertmanager | 配置环境、业务、实例和共享端点标签，按需包含静默或抑制告警。保存后在总览验收告警同步，在诊断详情核对关联证据。 |
| Grafana | 使用 Service Account Token 测试连接，在表单选择仪表盘并保存。用相同实例范围的诊断记录确认仪表盘入口。 |

查询正常但没有证据时，检查集群和命名空间范围、资源标签或字段及查询窗口。地址未获允许时核对审批清单、HTTPS 基地址和代理前缀。证书或认证失败时，从 API Pod 所在网络排查信任链、连通性和凭据。更多配置和故障处理见[资源管理设计](resource-management/design.md)。

已有使用环境变量接入的 Prometheus、VictoriaLogs 和 Alertmanager 仍由代码兼容。向控制台迁移时，在同一实例验证来源与证据后，再按本次发布计划清理旧配置。升级或者迁移数据时，同时保留 SQLite 和凭据加密所需文件。

## 部署 HolmesGPT

建议先接一条 Zabbix 告警，跑通后再扩展到 Alertmanager、Finding、事件 RCA 和 Wiki。下面统一使用 `holmes-pilot` 命名空间、`holmes-pilot` Helm 发布名、`accurate-model` 模型别名。命令可以逐段复制。在一台已配置集群访问权限的管理机上，从 kube-aiops 仓库根目录执行。需要 Helm 3、kubectl、Python 3 和可用的 OpenAI API Key。

```bash
kubectl config current-context
kubectl -n kube-aiops-system get deployment kube-aiops-api kube-aiops-web
helm version --short
python3 --version
```

确认当前集群正确，且 API、Web 已部署为包含 Holmes 接入的版本。Finding 和事件 RCA 还需先启用[现有诊断模型](#启用诊断详情的-ai-分析)，保留回退路径。

### 1. 创建命名空间、服务账号与模型 Secret

```bash
kubectl create namespace holmes-pilot --dry-run=client -o yaml | kubectl apply -f -
kubectl -n holmes-pilot apply -f - <<'YAML'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: holmes-model-only
automountServiceAccountToken: false
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-portal-api
spec:
  podSelector:
    matchLabels:
      app: holmes
  policyTypes:
    - Ingress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-aiops-system
          podSelector:
            matchLabels:
              app.kubernetes.io/name: kube-aiops-api
      ports:
        - protocol: TCP
          port: 5050
YAML
```

下一条命令会提示输入 OpenAI API Key。粘贴后按 Enter，输入不回显。Secret 的名称是 `holmes-model`，键名是 `api-key`。命令需要在交互式终端中运行，确认提示并输入非空密钥。

```bash
python3 -c 'import getpass, sys; key = getpass.getpass("OpenAI API Key: ").strip(); sys.stdout.write(key)' |
  kubectl -n holmes-pilot create secret generic holmes-model --from-file=api-key=/dev/stdin
kubectl -n holmes-pilot get secret holmes-model
```

此处的入站策略依赖集群 CNI 执行 NetworkPolicy。生产还需按现有网络规范限制 Holmes 的出站访问，只允许 DNS、模型和获准的数据源。

### 2. 安装 Holmes 并验证模型

把这段整块复制到终端，会在当前目录创建 `holmes-values.yaml`。其中 `OPENAI_API_KEY` 由 Pod 从 Secret 自动读取，终端无需设置环境变量。

```bash
cat > holmes-values.yaml <<'YAML'
createServiceAccount: false
customServiceAccountName: holmes-model-only
enableTelemetry: false
mcp_servers: {}
toolsets:
  kubernetes/core: {enabled: false}
  kubernetes/logs: {enabled: false}
  robusta: {enabled: false}
  internet: {enabled: false}
  prometheus/metrics: {enabled: false}
  runbook: {enabled: false}
  connectivity_check: {enabled: false}
  bash: {enabled: false}
additionalEnvVars:
  - name: OPENAI_API_KEY
    valueFrom:
      secretKeyRef:
        name: holmes-model
        key: api-key
modelList:
  accurate-model:
    model: openai/gpt-4.1
    api_key: "{{ env.OPENAI_API_KEY }}"
YAML

helm repo add robusta https://robusta-charts.storage.googleapis.com
helm repo update robusta
helm upgrade --install holmes-pilot robusta/holmes --version 0.24.3 \
  -n holmes-pilot -f holmes-values.yaml --atomic --timeout 5m
kubectl -n holmes-pilot rollout status deployment/holmes-pilot-holmes --timeout=180s
kubectl -n holmes-pilot get pods,svc
```

正常时 Deployment 就绪，Service 为 `holmes-pilot-holmes`，端口为 `80`。下面从容器内通过本地端口检查 readiness 和模型调用，避免开放额外入口。

```bash
kubectl -n holmes-pilot exec -i deployment/holmes-pilot-holmes -- python3 - <<'PY'
import json
from urllib.request import Request, urlopen

with urlopen("http://127.0.0.1:5050/readyz", timeout=5) as response:
    print("Ready:", response.status)

request = Request(
    "http://127.0.0.1:5050/api/chat",
    data=json.dumps({"ask": "Reply with the word OK.", "model": "accurate-model"}).encode(),
    headers={"Content-Type": "application/json"},
)
with urlopen(request, timeout=45) as response:
    result = json.load(response)
    assert result.get("analysis"), "Holmes returned an empty analysis"
    print("Model:", response.status, result["analysis"])
PY
```

看到 `Ready: 200` 和 `Model: 200` 后继续。需要替换成内网 vLLM 时，按实际模型和 OpenAI 兼容 API 地址修改 Chart 配置，并复跑本节检查。用于 Finding 初步复核和事件 RCA 时，还要用实际模型验证 `response_format`。如需让 Holmes 查实时指标或日志，按试点范围单独配置只读数据源和凭据。本例使用 Portal 已有证据，工具均关闭。[Chart 配置](https://holmesgpt.dev/0.24.3/reference/helm-configuration/) · [HTTP API](https://holmesgpt.dev/0.24.3/reference/http-api/)

### 3. 从 Portal 找到一条告警的真实 ID

1. 登录 Portal，打开 **诊断控制台 → 诊断总览**，找到一条要测试的 Zabbix 告警。
2. 按 F12 打开浏览器开发者工具，切到 **网络（Network）**，刷新总览。在筛选框搜索 `zabbix/problems`。
3. 点击该 GET 请求，在 **响应（Response）** 中按告警标题找到对应记录。记下该条的 `sourceId` 和 `scope.environmentId`、`scope.businessId`。须复制返回的 ID，页面上的环境名、业务名和告警的 `id` 不能代替它们。

### 4. 写入绑定并启用 Portal

先复制以下命令创建文件。将 `zbx-prod`、`prod`、`orders` 换成上一步找到的三个 ID。服务名、命名空间和模型名沿用前面示例时，`endpoint` 和 `model` 可以原样使用。Portal API 会自动给 `endpoint` 追加 `/api/chat`。

```bash
cat > bindings.json <<'JSON'
[
  {
    "source": "zabbix",
    "sourceId": "zbx-prod",
    "environmentId": "prod",
    "businessId": "orders",
    "endpoint": "http://holmes-pilot-holmes.holmes-pilot.svc",
    "model": "accurate-model"
  }
]
JSON
nano bindings.json
python3 -m json.tool bindings.json >/dev/null
```

在 nano 中按 Ctrl+O 保存、Enter 确认文件名、Ctrl+X 退出。JSON 校验命令无输出即表示格式正确。一次只写一条告警范围，先完成页面验收。

首次部署可以直接创建绑定 Secret。如果集群已经配置过 Holmes，先找回发布时使用的原始 `bindings.json`，将本条追加到现有数组，保留其他范围。短链路试点使用上面的内部 ClusterIP 地址。生产使用内部 HTTPS 时，填实际网关根地址，确保 API 信任证书。若网关要求 `X-API-Key`，对应条目追加 `apiKey`，并通过 Secret 管理该文件。

```bash
kubectl -n kube-aiops-system create secret generic kube-aiops-holmes-bindings \
  --from-file=bindings.json=./bindings.json --dry-run=client -o yaml |
  kubectl apply -f -
kubectl -n kube-aiops-system patch deployment kube-aiops-api --type=strategic \
  --patch-file deploy/examples/holmes/api-bindings.patch
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api --timeout=180s
```

补丁会挂载 Secret 并设置 API 读取路径。后续通过 GitOps 或流水线发布时，将补丁中的挂载和配置合入正式 Deployment，否则下一次发版可能覆盖本次设置。健康告警调查最多等待 Holmes 45 秒。若实际入口超时更短，为 `/api/v1/diagnostics/health/` 留足等待时间，例如 Gateway 后端 55 秒、请求 60 秒，前置 NGINX 读取超时至少 60 秒。

### 5. 在页面验收并扩展到其他来源

给试点账号授予目标范围的数据源读取权限和 `diagnosis.health.investigate`。打开 **诊断总览 → 告警详情 → AI 深度调查**，确认有分析结果。对比启用前后总览的相同筛选条件，统计和最近诊断仍来自原数据源。

扩展时，在 `bindings.json` 数组追加对应记录，重新执行第 4 步的 Secret 更新命令。字段按实际值精确匹配，表中未列出的范围字段可以省略。

| `source` | `sourceId` | 需要填写的范围 |
| --- | --- | --- |
| `zabbix` | 告警响应的 `sourceId` | `environmentId`、`businessId` |
| `alertmanager` | 在浏览器网络面板搜索 `observability/alerts`，取告警的 `sourceId` | `scope` 中的 `environmentId`、`businessId`、`clusterId`，告警的 `namespace` 有值时也要填写 |
| `finding` | Finding 所在集群 ID，与 `clusterId` 一致 | `clusterId`、实际 `namespace` |
| `incident` | 事件所在集群 ID，与 `clusterId` 一致 | `environmentId`、`businessId`、`clusterId`、实际 `namespace` |
| `wiki` | 固定为 `published-excerpts` | 使用独立 Holmes 实例的 `endpoint`，禁用外部查询、MCP 和执行工具 |

例如 Alertmanager 条目可以写成下面这样。把返回的 ID 和命名空间逐个替换，条目与已有 Zabbix 对象之间加一个逗号。

```json
{
  "source": "alertmanager",
  "sourceId": "am-prod",
  "environmentId": "prod",
  "businessId": "orders",
  "clusterId": "prod-sh-01",
  "namespace": "orders",
  "endpoint": "http://holmes-pilot-holmes.holmes-pilot.svc",
  "model": "accurate-model"
}
```

这个地址适用于同一个无工具试点实例。如果两个范围配置了不同的取证工具和凭据，则分别部署并填写各自的服务地址。每个实例用独立命名空间，重复第 1–2 步时替换命名空间和 release 名称。这个 Chart 的 Service 通过 `app: holmes` 匹配 Pod，同一命名空间的多套 release 会混用后端。Wiki 应只接收 Portal 已授权的发布章节。任何启用了只读数据源工具的实例，其凭据和访问范围要与绑定一致。

### 6. 排错与回滚

```bash
kubectl -n holmes-pilot get pods,svc,endpointslice
kubectl -n holmes-pilot logs deployment/holmes-pilot-holmes --since=10m --tail=100
kubectl -n kube-aiops-system logs deployment/kube-aiops-api --since=10m --tail=100
```

| 现象 | 检查 |
| --- | --- |
| Holmes 容器未就绪 | 用 `kubectl -n holmes-pilot get events --sort-by=.lastTimestamp` 查看镜像拉取和 Secret 挂载错误。 |
| `HOLMES_SCOPE_UNCONFIGURED` | 对照告警的来源 ID、环境、业务、集群和命名空间，确认 Secret 已更新。 |
| `HOLMES_UNAVAILABLE` 或调用超时 | 先重跑第 2 步检查模型，再核对 Service 地址、NetworkPolicy、模型别名和入口代理时限。 |
| 账号无法发起调查 | 核对数据源读取权限和 `diagnosis.health.investigate` 的范围。 |

撤掉某个范围时，从绑定数组删掉该条并重新应用 Secret，等待挂载更新后在页面复验。只部署了本例一个范围时，可以将文件内容改成 `[]` 再重新应用。已有 Finding、事件 RCA 和 Wiki 可回到各自的既有模型路径。健康告警继续显示，发起调查时会提示该范围未配置。

## 排查与清理

| 现象 | 核对项 |
| --- | --- |
| 下载依赖失败 | 主机能否通过 HTTPS 访问 `go.dev`、`nodejs.org`、`dl.k8s.io`、`get.helm.sh` 及下载重定向地址。也可提前安装工具并加入 `PATH` |
| 安装提示缺少 Secret | 当前 context、目标 Namespace 以及 `make bootstrap-secret` 的输出 |
| Helm 等待就绪，Pod 出现 `ErrImagePull` 或 `ImagePullBackOff` | 安装日志中的镜像引用、Pod 事件，以及 Pod 所在节点到对应仓库的连通性、TLS 和拉取凭据 |
| Engine 已就绪但 Finding 为空 | 分析周期、Provider 凭据、K8sGPT Result 及 Result 的实例标签 |
| API 的 `/readyz` 返回 503 | Result CRD、K8sGPT Namespace 和 API ServiceAccount 的 Result 读取权限 |
| 页面打开但 API 返回 HTML 或 404 | 同源入口的 `/api/v1/` 转发路径和 API Service 端口 |
| LDAP 搜索密码或测试按钮未出现 | 检查 API 与 Web 是否来自同一源码版本。更新镜像后确认两个 Deployment 已就绪，再重新打开认证配置 |
| LDAP 配置无法保存 | 在浏览器 Network 检查 `PUT /api/v1/admin/ldap`。成功应返回 204，测试请求应返回 JSON。更换 LDAP 地址或搜索 DN 后重新填写密码，并确认 API 使用 SQLite 身份存储 |
| 证据来源为 `disabled` | 对应 `*_URL` 是否已设置 |
| 证据来源为 `partial` 或 `unavailable` | Pod 到上游的连通性、TLS、凭据、集群标签和查询窗口 |
| API 在生产模式退出 | 后端启动日志中的安全组合校验及可信认证、授权接入情况 |

清理 Portal 时，先下线两条专用 HTTPRoute，再删除本次发布的 Deployment、Service 和 NetworkPolicy。按发布清单核对资源归属后再删除；共享 Gateway、证书和其他系统的资源由平台维护。

在已有测试集群中，`make clean-demo` 清理故障样例，`make uninstall` 清理 K8sGPT Release、实例和项目 RBAC。默认保留 Secret、Namespace 和 CRD。使用 `PURGE_SECRET=true make uninstall` 可同时删除 Provider Secret。
