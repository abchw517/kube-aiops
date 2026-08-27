SHELL := /usr/bin/env bash

DEMO_NAMESPACE ?= k8sgpt-demo
OPERATOR_VERSION ?= 0.2.27

.PHONY: help preflight bootstrap bootstrap-secret install verify demo clean-demo status results uninstall e2e

help:
	@echo "kube-aiops Phase 1.1"
	@echo
	@echo "常用命令:"
	@echo "  make preflight     执行 Shell/YAML/Secret/RBAC 静态检查"
	@echo "  make bootstrap     创建固定 Namespace（无其它集群变更）"
	@echo "  make bootstrap-secret 使用 OPENAI_TOKEN 创建 AI Secret"
	@echo "  make install       安装/升级 K8sGPT Operator、应用 RBAC Hardening、检查 Secret 并部署 K8sGPT CR"
	@echo "  make verify        验证 Operator、CRD、RBAC 安全边界、K8sGPT CR 与 Result CR"
	@echo "  make demo          部署 ImagePullBackOff / CrashLoopBackOff / PVC Pending 故障样例"
	@echo "  make clean-demo    删除故障样例 Namespace"
	@echo "  make status        查看 Phase 1.1 关键资源状态"
	@echo "  make results       查看 K8sGPT Result CR"
	@echo "  make uninstall     卸载 Phase 1.1，默认保留 Secret、Namespace 和 CRD"
	@echo
	@echo "可覆盖变量:"
	@echo "  OPERATOR_VERSION=$(OPERATOR_VERSION)"
	@echo "  STRICT_RESULTS=true  # verify 时要求至少存在一个 Result"
	@echo "  PURGE_SECRET=true    # uninstall 时同时删除 AI Secret"
	@echo "  PURGE_NAMESPACE=true # uninstall 时同时删除 Namespace"

preflight:
	@bash ./preflight.sh

bootstrap:
	@kubectl apply -f deploy/k8sgpt/namespace.yaml

bootstrap-secret: bootstrap
	@test -n "$${OPENAI_TOKEN:-}" || { echo "ERROR: OPENAI_TOKEN 未设置" >&2; exit 1; }
	@kubectl create secret generic k8sgpt-openai-secret \
		-n k8sgpt-operator-system \
		--from-literal=openai-api-key="$${OPENAI_TOKEN}" \
		--dry-run=client -o yaml | kubectl apply -f -

install:
	@OPERATOR_VERSION="$(OPERATOR_VERSION)" bash ./install.sh

verify:
	@STRICT_RESULTS="$${STRICT_RESULTS:-false}" bash ./verify.sh

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
	@kubectl get pods -n k8sgpt-operator-system -o wide || true
	@echo
	@echo "== K8sGPT CR =="
	@kubectl get k8sgpt -n k8sgpt-operator-system || true
	@echo
	@echo "== Result CR =="
	@kubectl get results -n k8sgpt-operator-system || true

results:
	@kubectl get results -n k8sgpt-operator-system -o wide

uninstall:
	@PURGE_SECRET="$${PURGE_SECRET:-false}" PURGE_NAMESPACE="$${PURGE_NAMESPACE:-false}" PURGE_DEMO="$${PURGE_DEMO:-false}" bash ./uninstall.sh

e2e:
	@bash ./tests/e2e-kind.sh
