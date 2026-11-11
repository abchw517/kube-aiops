package main

import (
	"github.com/abchw517/kube-aiops/internal/alertmanager"
	"github.com/abchw517/kube-aiops/internal/config"
	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/prometheus"
	"github.com/abchw517/kube-aiops/internal/victorialogs"
)

func buildCorrelator(cfg config.Config, events correlation.EventSource) (*correlation.Service, error) {
	metrics, err := prometheus.New(cfg.Prometheus)
	if err != nil {
		return nil, err
	}
	logs, err := victorialogs.New(cfg.VictoriaLogs)
	if err != nil {
		return nil, err
	}
	alerts, err := alertmanager.New(cfg.Alertmanager)
	if err != nil {
		return nil, err
	}
	return correlation.NewService(correlation.ServiceOptions{
		Events: events, Prometheus: metrics, VictoriaLogs: logs, Alertmanager: alerts,
	})
}
