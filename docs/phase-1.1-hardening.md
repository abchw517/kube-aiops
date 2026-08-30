# Phase 1.1 P1 Hardening

本文记录 Phase 1.1 生产准入审查后独立实施的 P1 加固项。

## 加固范围

| 控制项 | 实现 |
|---|---|
| RBAC 静态扫描 | 扫描仓库所有 YAML 中的 `Role` 与 `ClusterRole` |
| 集群级资源所有权 | 项目 label/annotation，接管或删除前强制校验 |
| GitHub Actions | 第三方 Action 固定到完整 commit SHA |
| Kind node 镜像 | Kubernetes v1.34.8 固定上游发布 digest |
| Operator 基线 | 默认升级到 v0.2.29 |
| E2E 异常路径 | 验证外部同名 RBAC 不被接管或删除 |

## 第二轮 P1 深度加固

| 问题 | 修复与验收 |
|---|---|
| 单个 label/annotation 可伪造所有权 | 改为 owner、part-of、instance 三元精确身份；legacy 仅允许安装器显式迁移并校验语义指纹 |
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

本轮 Kind E2E 还覆盖所有权伪造、legacy 指纹漂移、同名外部 Release、Lease
heartbeat/fencing/CAS 以及回滚状态保留。Provider 功能 E2E 仍保持独立人工门禁，
不能用占位 Token 替代。

## Operator 版本选择

`v0.2.28` 包含以下与本项目相关的改进：

- gRPC 依赖安全更新；
- 防止 Prompt Injection 导致跨 Namespace Mutation；
- 将分析错误写入 K8sGPT status，便于严格验证与告警。

`v0.2.29` 在此基础上增加策略门控 Auto Remediation。本项目仍执行最小权限
基线：Mutation 与 Auto Remediation 禁用，ServiceAccount 不具备写权限。

## Required Status Checks

`main` 分支应禁止绕过以下三个检查：

- `Preflight / Lint / RBAC`
- `Secret Scan`
- `Kubernetes v1.34 Kind E2E`

仓库管理员在 GitHub `Settings → Rules → Rulesets` 创建面向 `main` 的规则：

1. Require a pull request before merging；
2. Require status checks to pass；
3. 选择以上三个检查并启用 branch must be up to date；
4. 禁止直接 Push 与绕过规则，仅保留必要的紧急管理员通道。

规则属于仓库控制面配置，不存储在 Git 工作树中。合并前应通过 GitHub API 或
Settings 页面确认 Ruleset 已生效。
