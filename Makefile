SHELL := /usr/bin/env bash

NAMESPACE ?= k8sgpt-operator-system
DEMO_NAMESPACE ?= k8sgpt-demo
OPERATOR_VERSION ?= 0.2.27

.PHONY: help preflight install verify demo clean-demo status results uninstall

help:
	@echo "kube-aiops Phase 1.1"
	@echo
	@echo "常用命令:"
	@echo "  make preflight     执行 Shell/YAML/Secret/RBAC 静态检查"
	@echo "  make install       安装/升级 K8sGPT Operator、应用 RBAC Hardening、检查 Secret 并部署 K8sGPT CR"
	@echo "  make verify        验证 Operator、CRD、RBAC 安全边界、K8sGPT CR 与 Result CR"
	@echo "  make demo          部署 ImagePullBackOff / CrashLoopBackOff / PVC Pending 故障样例"
	@echo "  make clean-demo    删除故障样例 Namespace"
	@echo "  make status        查看 Phase 1.1 关键资源状态"
	@echo "  make results       查看 K8sGPT Result CR"
	@echo "  make uninstall     卸载 Phase 1.1，默认保留 Secret、Namespace 和 CRD"
	@echo
	@echo "可覆盖变量:"
	@echo "  NAMESPACE=$(NAMESPACE)"
	@echo "  OPERATOR_VERSION=$(OPERATOR_VERSION)"
	@echo "  STRICT_RESULTS=true  # verify 时要求至少存在一个 Result"
	@echo "  PURGE_SECRET=true    # uninstall 时同时删除 AI Secret"
	@echo "  PURGE_NAMESPACE=true # uninstall 时同时删除 Namespace"

preflight:
	@bash ./preflight.sh

install:
	@NAMESPACE="$(NAMESPACE)" OPERATOR_VERSION="$(OPERATOR_VERSION)" bash ./install.sh

verify:
	@NAMESPACE="$(NAMESPACE)" STRICT_RESULTS="$${STRICT_RESULTS:-false}" bash ./verify.sh

demo:
	@kubectl apply -f deploy/k8sgpt/demo/namespace.yaml
	@kubectl apply -f deploy/k8sgpt/demo/imagepullbackoff.yaml
	@kubectl apply -f deploy/k8sgpt/demo/crashloopbackoff.yaml
	@kubectl apply -f deploy/k8sgpt/demo/pvc-pending.yaml
	@echo
	@echo "Demo 已部署。等待 K8sGPT 分析周期后可执行:"
	@echo "  make results"
	@echo "  STRICT_RESULTS=true make verify"

clean-demo:
	@kubectl delete namespace "$(DEMO_NAMESPACE)" --ignore-not-found=true

status:
	@echo "== Operator / Engine =="
	@kubectl get pods -n "$(NAMESPACE)" -o wide || true
	@echo
	@echo "== K8sGPT CR =="
	@kubectl get k8sgpt -n "$(NAMESPACE)" || true
	@echo
	@echo "== Result CR =="
	@kubectl get results -n "$(NAMESPACE)" || true

results:
	@kubectl get results -n "$(NAMESPACE)" -o wide

uninstall:
	@NAMESPACE="$(NAMESPACE)" PURGE_SECRET="$${PURGE_SECRET:-false}" PURGE_NAMESPACE="$${PURGE_NAMESPACE:-false}" bash ./uninstall.sh
