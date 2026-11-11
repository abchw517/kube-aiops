package prometheus

import "github.com/abchw517/kube-aiops/internal/correlation"

type metricDefinition struct {
	name       string
	unit       correlation.SignalUnit
	expression string
	instant    bool
}

var metricCatalog = map[string][]metricDefinition{
	"Pod": {
		{
			name:       "pod_cpu_usage",
			unit:       correlation.UnitCores,
			expression: `sum(max by (container,cpu) (rate(container_cpu_usage_seconds_total{%s,container!="",container!="POD"}[5m])))`,
		},
		{
			name:       "pod_memory_working_set",
			unit:       correlation.UnitBytes,
			expression: `sum(max by (container) (container_memory_working_set_bytes{%s,container!="",container!="POD"}))`,
		},
		{
			name:       "pod_restarts_5m",
			unit:       correlation.UnitCount,
			expression: `sum(max by (container) (increase(kube_pod_container_status_restarts_total{%s}[5m])))`,
		},
		{
			name:       "pod_network_receive",
			unit:       correlation.UnitBytesPerSecond,
			expression: `sum(max by (interface) (rate(container_network_receive_bytes_total{%s,interface!="",interface!="lo"}[5m])))`,
		},
		{
			name:       "pod_network_transmit",
			unit:       correlation.UnitBytesPerSecond,
			expression: `sum(max by (interface) (rate(container_network_transmit_bytes_total{%s,interface!="",interface!="lo"}[5m])))`,
		},
	},
	"Deployment": {
		{
			name:       "workload_ready_replicas",
			unit:       correlation.UnitCount,
			expression: `max(kube_deployment_status_replicas_ready{%s})`,
			instant:    true,
		},
	},
	"Node": {
		{
			name:       "node_pressure_indicator",
			unit:       correlation.UnitRatio,
			expression: `max(kube_node_status_condition{%s,status="true",condition=~"MemoryPressure|DiskPressure|PIDPressure"})`,
			instant:    true,
		},
	},
}
