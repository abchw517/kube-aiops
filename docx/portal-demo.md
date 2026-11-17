# Web Portal 本地 Demo

## 启动

在仓库根目录运行。脚本优先复用可用的 Go（最低 1.26.0）和 Node.js（22 及以上，含 `npm`、`npx`）。缺失、版本过低或工具链损坏时，Linux x86_64 和 arm64 主机会自动下载并校验 Go 1.26.5 和 Node.js 22.19.0 官方二进制包。主机需预先提供 Bash、OpenSSL、curl 和常用系统命令（如 `awk`、`tar`、`sha256sum`）。

```bash
make portal-demo
```

可先运行 `make portal-deps` 单独检查和准备工具链。工具默认保存在 `$HOME/.local/share/kube-aiops/tools`，可通过 `KUBE_AIOPS_TOOLS_DIR` 指定其他目录。Demo 使用当前进程的 `PATH`，无需修改系统配置。首次构建前端还需访问 npm registry 以取得 `esbuild`，已有 npm 缓存时可复用。

打开 <http://127.0.0.1:4173/#/findings>。按 Ctrl+C 停止。进程退出时会清理临时证书和后端进程。

Portal 默认监听 `127.0.0.1:4173`。可在 `make` 前指定监听地址和端口。需要从其他机器访问时，可让 Portal 监听所有 IPv4 网卡：

```bash
PORTAL_DEMO_HOST=0.0.0.0 \
PORTAL_DEMO_PORT=4173 \
make portal-demo
```

从其他机器打开 `http://<服务器IP>:4173/#/findings`。Go API 和 HTTPS 样例服务继续监听回环地址，由 Portal 转发 API 请求。

如果端口被占用，可分别指定页面端口和 Go API 端口。

```bash
make portal-demo PORTAL_DEMO_PORT=4180 PORTAL_DEMO_API_PORT=18083
```

页面的 **演示数据** 标识表示当前使用本地样例。界面和样例诊断使用简体中文，资源名、指标名和告警名保留原值。接入真实集群后，诊断说明和证据摘要沿用数据源提供的语言。Kubernetes Result、Events、Prometheus、VictoriaLogs 和 Alertmanager 的演示数据由本地 HTTPS 样例服务提供。运行时无需 kubeconfig、Kubernetes 集群、AI Provider 或监控系统凭据。

## 演示路径

1. 在列表中查看 2 条诊断结果。选择 `payments` 命名空间，确认列表只显示 checkout Pod。
2. 打开 **checkout-api 容器反复重启（CrashLoopBackOff）**，查看诊断详情和四类证据。Kubernetes Events 有重启退避记录，Prometheus 有内存和重启指标，VictoriaLogs 有 OOM 分类计数，Alertmanager 有对应告警。
3. 清空过滤条件并打开 **queue-worker 就绪副本数低于目标**。核对 Deployment 目标、就绪副本指标和告警，VictoriaLogs 卡片显示该时间窗没有证据。

## 自动验收

```bash
make portal-demo-check
```

验收通过同一个页面入口检查静态资源、后端就绪、列表与命名空间过滤、Finding 详情、四类证据、Deployment 相关性和写请求拒绝。成功后自动停止服务。

现有 `make demo` 用于向 Kubernetes 集群部署故障工作负载。它适用于已准备好 Operator、K8sGPT Result 和部署环境的集群验收。
