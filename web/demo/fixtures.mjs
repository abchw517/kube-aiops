const anchor = Math.floor(Date.now() / 60_000) * 60;
const timestamp = (seconds) => new Date(seconds * 1000).toISOString();
const resultBase = "/apis/core.k8sgpt.ai/v1alpha1/namespaces/k8sgpt-operator-system/results";

const cases = [
  {
    id: "demo-checkout-crash",
    resource: { apiVersion: "v1", kind: "Pod", namespace: "payments", name: "checkout-api" },
    time: anchor,
    problem: "checkout-api 容器反复重启（CrashLoopBackOff）",
    details: "checkout-api 容器反复重启，Kubernetes 报告重启退避，样例日志记录了内存不足错误。请查看 Prometheus 证据中的内存工作集和重启次数。",
    reason: "BackOff",
    message: "checkout-api 容器重启进入退避状态",
    alert: "CheckoutPodCrashLooping",
    status: { phase: "Running" },
  },
  {
    id: "demo-worker-replicas",
    resource: { apiVersion: "apps/v1", kind: "Deployment", namespace: "platform", name: "queue-worker" },
    time: anchor - 120,
    problem: "queue-worker 就绪副本数低于目标",
    details: "queue-worker 的 3 个副本中只有 1 个就绪。请先查看 Deployment 事件和就绪副本指标，再排查调度或就绪探针异常。",
    reason: "FailedCreate",
    message: "queue-worker 发布尚未达到期望副本数",
    alert: "WorkerReplicasUnavailable",
    status: { replicas: 3, readyReplicas: 1, availableReplicas: 1 },
  },
];

function resultFor(item) {
  return {
    metadata: {
      name: item.id,
      namespace: "k8sgpt-operator-system",
      creationTimestamp: timestamp(item.time),
      labels: {
        "k8sgpts.k8sgpt.ai/name": "k8sgpt-engine",
        "k8sgpts.k8sgpt.ai/namespace": "k8sgpt-operator-system",
      },
    },
    spec: { targetRef: item.resource, error: [{ text: item.problem }], details: item.details },
  };
}

function eventFor(item) {
  return {
    metadata: { name: `${item.id}-event`, uid: `${item.id}-event` },
    involvedObject: item.resource,
    reason: item.reason,
    message: item.message,
    type: "Warning",
    firstTimestamp: timestamp(item.time - 600),
    lastTimestamp: timestamp(item.time - 60),
    count: item.resource.kind === "Pod" ? 8 : 2,
  };
}

function alertFor(item, index) {
  return {
    labels: {
      alertname: item.alert,
      namespace: item.resource.namespace,
      [item.resource.kind.toLowerCase()]: item.resource.name,
      severity: item.resource.kind === "Pod" ? "critical" : "warning",
    },
    startsAt: timestamp(item.time - 300),
    endsAt: timestamp(item.time + 600),
    fingerprint: (index + 1).toString(16).padStart(16, "0"),
    status: { state: "active" },
  };
}

const podMetrics = new Map([
  ["container_cpu_usage_seconds_total", ["0.18", "0.24"]],
  ["container_memory_working_set_bytes", ["201326592", "268435456"]],
  ["kube_pod_container_status_restarts_total", ["2", "8"]],
  ["container_network_receive_bytes_total", ["7800", "9240"]],
  ["container_network_transmit_bytes_total", ["5000", "6120"]],
]);

function queryResponse(resultType, result) {
  return { status: "success", data: { resultType, result } };
}

export function fixtureResponse(url) {
  const path = url.pathname;
  if (path === resultBase) return { metadata: {}, items: cases.map(resultFor) };
  if (path.startsWith(`${resultBase}/`)) {
    const item = cases.find((entry) => path === `${resultBase}/${entry.id}`);
    return item ? resultFor(item) : undefined;
  }
  if (path === "/api/v1/namespaces") {
    return { items: cases.map(({ resource }) => ({ metadata: { name: resource.namespace } })) };
  }
  const eventCase = cases.find(({ resource }) => path === `/api/v1/namespaces/${resource.namespace}/events`);
  if (eventCase) return { items: [eventFor(eventCase)] };

  const resourceCase = cases.find(({ resource }) => {
    const group = resource.kind === "Pod" ? "/api/v1" : "/apis/apps/v1";
    const kind = resource.kind.toLowerCase();
    return path === `${group}/namespaces/${resource.namespace}/${kind}s/${resource.name}`;
  });
  if (resourceCase) {
    const { apiVersion, kind, name, namespace } = resourceCase.resource;
    return { apiVersion, kind, metadata: { name, namespace }, status: resourceCase.status };
  }

  const expression = url.searchParams.get("query") ?? "";
  if (path === "/api/v1/query_range") {
    const metric = [...podMetrics].find(([name]) => expression.includes(name));
    const values = metric && expression.includes('pod="checkout-api"')
      ? [{ metric: {}, values: [[anchor - 120, metric[1][0]], [anchor - 60, metric[1][1]]] }]
      : [];
    return queryResponse("matrix", values);
  }
  if (path === "/api/v1/query") {
    const values = expression.includes("kube_deployment_status_replicas_ready") && expression.includes('deployment="queue-worker"')
      ? [{ metric: {}, value: [anchor - 120, "1"] }]
      : [];
    return queryResponse("vector", values);
  }
  if (path === "/select/logsql/stats_query_range") {
    const values = expression.includes('="checkout-api"')
      ? [{ metric: { __name__: "out_of_memory" }, values: [[anchor - 120, "2"], [anchor - 60, "3"]] }]
      : [];
    return queryResponse("matrix", values);
  }
  if (path === "/api/v2/alerts") {
    const filters = url.searchParams.getAll("filter");
    return cases.map(alertFor).filter((alert) =>
      filters.every((filter) => Object.entries(alert.labels).some(([name, value]) => filter === `${name}="${value}"`)));
  }
  return undefined;
}
