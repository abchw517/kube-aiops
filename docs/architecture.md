# Phase 1.1 架构设计

## 目标

Phase 1.1 只建设 K8sGPT Engine，不建设 Web Portal、不接入 Prometheus/Loki/Alertmanager，也不启用任何自动修复。

核心目标：

> 在最小权限模型下，让 K8sGPT 能够读取 Kubernetes 资源、执行 AI 诊断，并将结果写入 Result CR。

## 架构

```text
Kubernetes Cluster
│
├── k8sgpt-operator-system
│   │
│   ├── K8sGPT Operator
│   │      │
│   │      └── reconcile K8sGPT CR
│   │
│   ├── K8sGPT CR
│   │
│   ├── K8sGPT Engine
│   │      │
│   │      ├── ServiceAccount: k8sgpt
│   │      └── ClusterRole: k8sgpt-clusterrole
│   │
│   └── Result CR
│
└── Application Namespaces
    ├── Pod
    ├── Deployment
    ├── StatefulSet
    ├── Service
    ├── PVC
    ├── Job/CronJob
    ├── Ingress
    └── HPA
```

## 数据链路

```text
Kubernetes Resource
        │
        ▼
K8sGPT Analyzer
        │
        ▼
Anonymization
        │
        ▼
AI Provider
        │
        ▼
K8sGPT Result
        │
        ▼
Result CR
        │
        ▼
Phase 1.2 Portal Backend
```

## Phase 1.1 与后续阶段边界

Phase 1.1：

- Kubernetes Resource ReadOnly
- K8sGPT Analyzer
- AI Explain
- Result CR
- RBAC Hardening
- Demo Fault Injection

不包含：

- Web Portal
- Chat UI
- Prometheus RCA
- Loki Log RCA
- Alertmanager
- Event Pipeline
- MCP Tool Calling
- Mutation
- Auto Remediation

## Result CR 作为阶段契约

Phase 1.2 Backend 不解析 K8sGPT 日志，而是通过 Kubernetes API 读取 Result CR。

```text
K8sGPT Result CR
       │
       ▼
Result Adapter
       │
       ▼
Internal Finding Model
       │
       ▼
Portal API
```

这样后续 Prometheus、Loki、Alertmanager、JMX、eBPF 等数据源都可以统一映射到 Finding 模型，而不会让 Portal 与 K8sGPT 的具体输出格式强耦合。
