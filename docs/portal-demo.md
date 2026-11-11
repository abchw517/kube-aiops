# Web Portal 本地 Demo

## 启动

在仓库根目录运行，依赖 Go 1.26.5、Node.js 22+、npm、OpenSSL、curl 和 Bash。

```bash
make portal-demo
```

打开 <http://127.0.0.1:4173/#/findings>。按 Ctrl+C 停止。进程退出时会清理临时证书和后端进程。

如果端口被占用，可分别指定页面端口和 Go API 端口。

```bash
make portal-demo PORTAL_DEMO_PORT=4180 PORTAL_DEMO_API_PORT=18083
```

页面的 **Demo data** 标识表示当前使用本地样例。浏览器访问真实 Web Portal 和 Go 后端。Kubernetes Result、Events、Prometheus、VictoriaLogs 和 Alertmanager 数据由本地 HTTPS 样例服务提供。服务监听回环地址，运行时无需 kubeconfig、Kubernetes 集群、AI Provider 或监控系统凭据。前端构建沿用仓库现有 npm 工具链。

## 演示路径

1. 在列表中查看 2 条 Finding。选择 `payments` 命名空间，确认列表只显示 checkout Pod。
2. 打开 **CrashLoopBackOff in checkout-api**，查看 Finding 详情和四类证据。Kubernetes Events 有重启退避记录，Prometheus 有内存和重启指标，VictoriaLogs 有 OOM 分类计数，Alertmanager 有对应告警。
3. 清空过滤条件并打开 **Queue worker replicas below target**。核对 Deployment 目标、就绪副本指标和告警，VictoriaLogs 卡片显示该时间窗没有证据。

## 自动验收

```bash
make portal-demo-check
```

验收通过同一个页面入口检查静态资源、后端就绪、列表与命名空间过滤、Finding 详情、四类证据、Deployment 相关性和写请求拒绝。成功后自动停止服务。

现有 `make demo` 用于向 Kubernetes 集群部署故障工作负载。它适用于已准备好 Operator、K8sGPT Result 和部署环境的集群验收。
