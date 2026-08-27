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
