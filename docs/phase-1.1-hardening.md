# Phase 1.1 P1 Hardening

本文记录 Phase 1.1 生产准入审查后实施的加固项。Phase 1.1 功能范围已经冻结，
当前持续回归平台基线升级为 Kubernetes v1.36.4。

## 加固范围

| 控制项 | 实现 |
|---|---|
| RBAC 静态扫描 | 扫描仓库所有 YAML 中的 `Role` 与 `ClusterRole` |
| 集群级资源所有权 | 项目 label/annotation，接管或删除前强制校验 |
| GitHub Actions | 第三方 Action 固定到完整 commit SHA |
| Kind node 镜像 | Kubernetes v1.36.4 固定上游发布 digest |
| Kind CLI | v0.33.0 显式固定 |
| kubectl | v1.36.4 显式固定 |
| Go toolchain | Kubernetes release-1.36 对齐 Go 1.26.5 |
| Operator 基线 | v0.2.29 immutable digest |
| Engine 基线 | v0.4.37 immutable digest |
| E2E 异常路径 | 验证外部同名 RBAC 不被接管或删除 |
| 平台漂移 | `tests/platform-baseline-test.py` fail closed |

## 深度加固

| 问题 | 修复与验收 |
|---|---|
| 单个 label/annotation 可伪造所有权 | owner、part-of、instance 三元精确身份；legacy 仅允许安装器显式迁移并校验语义指纹 |
| 同名外部 Helm Release | 升级和卸载前验证 Chart 身份 |
| Binding 可能保留上游 subject/roleRef | post-renderer 在进入 API Server 前强制替换 rules、subjects、roleRef 和 ServiceAccount 安全字段 |
| Make 参数命令注入 | recipe 不再文本插入 Make 变量；Operator 版本在脚本中执行 allowlist 校验 |
| Token 暴露在进程 argv | Secret 经 stdin 创建；安装和验证只返回 key 是否存在，不读取值 |
| Provider 工作流分支可选 | 只有 `refs/heads/main` 才能进入 Environment job |
| Lease 长事务失锁和释放竞态 | heartbeat 持续续租、变更前 fencing、resourceVersion CAS 释放 |
| 回滚二次失败丢失快照 | 保留 `0700` 状态目录、operation metadata 和对象级恢复报告 |
| `make status` 假成功 | 独立可信状态检查，关键失败返回非零 |
| Verify/Result 错误被吞或错报 | 保留 stderr/rc，区分 Forbidden、timeout、NotFound 和 schema 错误；CR 单快照验证 |
| RBAC Binding 未静态检查 | lint 校验 roleRef 必须指向仓库受检 Role，subject 只允许具名 ServiceAccount |
| Pod 安全上下文不足 | Namespace 启用 PSA baseline enforce、restricted audit/warn；Operator/Engine 启用 seccomp、non-root、drop ALL、只读根文件系统 |
| Namespace 无网络边界 | egress 仅允许 DNS、HTTPS 和 Kubernetes API 端口 |
| 运行时镜像只有可变 tag | Operator、kube-rbac-proxy、Engine 使用 `tag@sha256` 固定 manifest digest |
| Helm index/Chart 可变 | 直接下载固定版本 Chart，并在 Helm 前校验内置 SHA256 |
| CI 名称与实际 Kubernetes 版本可能漂移 | v1.36 smoke 同时检查 Kind、kubectl、API Server、kubelet 精确版本 |
| Manifest 使用已删除 Kubernetes API | kubeconform 1.36 + removed API static denylist |
| Go CI 与容器构建版本可能漂移 | `go.mod toolchain go1.26.5` + Docker builder 1.26.5 + baseline guard |

Kind E2E 覆盖所有权伪造、legacy 指纹漂移、同名外部 Release、Lease
heartbeat/fencing/CAS、回滚状态保留以及 Phase 1.2 readonly API。Provider 功能
E2E 保持独立人工门禁，不能用占位 Token 替代。

## 回归审查修复

| 漏洞 | 修复与验收 |
|---|---|
| `replicas: 0` 被误判为 Ready | install、verify、status 均要求 Operator/Engine 期望副本数至少为 1 |
| 同名外部 Release 可通过只读验收 | verify、status 同时校验 Helm Chart 精确身份 |
| 安全资源只验存在性 | verify 对 ServiceAccount、ClusterRole/Binding、NetworkPolicy 执行运行时语义基线比对 |
| status 混入其它实例 Result | Result 查询固定使用当前 K8sGPT 实例标签选择器 |

对应负向测试覆盖零副本、外部 Chart 身份和运行时 RBAC 扩权漂移。

## Kubernetes v1.36 API 加固

Portal Backend 不使用 `client-go`，当前通过受限 HTTP client 调用稳定 API：

```text
/api/v1
/apis/apps/v1
```

持续 E2E 另外验证：

```text
/apis/rbac.authorization.k8s.io/v1
/apis/batch/v1
/apis/networking.k8s.io/v1
/apis/autoscaling/v2
```

`core.k8sgpt.ai/v1alpha1` 是 K8sGPT CRD API，不属于 Kubernetes built-in beta API。
平台检查禁止 `extensions/v1beta1`、`networking.k8s.io/v1beta1`、`policy/v1beta1`
等已删除 built-in API 回流。

## Operator / Engine 版本选择

Operator 保持 `v0.2.29`：其已有策略门控 Auto Remediation，但本项目继续禁用
Mutation 与 Auto Remediation，ServiceAccount 不具备业务写权限。

Engine 从 `v0.4.32` 升级到 `v0.4.37`，使用经独立确认的语义 tag digest。生产
基线不为了追逐最新 tag 而放弃不可变镜像约束；更高版本必须先获得可信 digest
并通过 Kubernetes v1.36 完整 E2E。

## Required Status Checks

`main` 分支应禁止绕过：

- `Preflight / Lint / RBAC`
- `Secret Scan`
- `Kubernetes v1.36 Kind E2E`

Ruleset 要求：

1. Require a pull request before merging；
2. Require status checks to pass；
3. 选择以上三个检查并启用 branch must be up to date；
4. 禁止直接 Push 与普通绕过；
5. v1.36 新检查实际成功后再替换旧 v1.34 Required Check，不通过降低保护完成切换。

规则属于仓库控制面配置，不存储在 Git 工作树中。合并前应通过 GitHub API 或
Settings 页面确认 Ruleset 已生效。
