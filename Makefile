SHELL := /usr/bin/env bash
BUILD_DIR ?= .build
API_BINARY ?= $(BUILD_DIR)/kube-aiops-api

.PHONY: help preflight bootstrap bootstrap-secret install verify demo clean-demo status results uninstall e2e e2e-provider platform-check platform-smoke api-fmt-check api-test api-build api-run api-contract-validate api-client-generate api-client-drift-check api-contract-check

help:
	@echo "kube-aiops"
	@echo
	@echo "Platform baseline: Kubernetes v1.36.4 / Kind v0.33.0 / Go 1.26.5"
	@echo "  make platform-check  校验 Kubernetes/Kind/Go/K8sGPT/CI 基线一致性"
	@echo "  make platform-smoke  创建临时 Kind 集群并验证 Kubernetes v1.36 API Server"
	@echo
	@echo "Phase 1.1 K8sGPT Engine:"
	@echo "  make preflight     执行 Shell/YAML/Secret/RBAC/Kubernetes Schema 静态检查"
	@echo "  make bootstrap     创建固定 Namespace（无其它集群变更）"
	@echo "  make bootstrap-secret 使用 OPENAI_TOKEN 创建 AI Secret"
	@echo "  make install       安装/升级 K8sGPT Operator、应用 RBAC Hardening、检查 Secret 并部署 K8sGPT CR"
	@echo "  make verify        验证 Operator、CRD、RBAC 安全边界、K8sGPT CR 与 Result CR"
	@echo "  make demo          部署 ImagePullBackOff / CrashLoopBackOff / PVC Pending 故障样例"
	@echo "  make clean-demo    删除故障样例 Namespace"
	@echo "  make status        查看 Phase 1.1 关键资源状态"
	@echo "  make results       查看 K8sGPT Result CR"
	@echo "  make uninstall     卸载 Phase 1.1，默认保留 Secret、Namespace 和 CRD"
	@echo "  make e2e-provider  使用受限 OPENAI_TOKEN 执行严格 Provider 功能 E2E"
	@echo
	@echo "Phase 1.2 Portal Backend:"
	@echo "  make api-fmt-check         检查 Go 代码格式"
	@echo "  make api-test              执行 Go 单元测试"
	@echo "  make api-build             编译 kube-aiops-api"
	@echo "  make api-run               本地运行 kube-aiops-api"
	@echo "  make api-contract-validate 校验 OpenAPI、路由、Go DTO 和敏感字段边界"
	@echo "  make api-client-generate   从 OpenAPI 生成 TypeScript Client"
	@echo "  make api-client-drift-check 检查 TypeScript 生成物是否漂移"
	@echo "  make api-contract-check    执行完整 Phase 1.2.4 Contract Gate"
	@echo
	@echo "可覆盖变量:"
	@echo "  OPERATOR_VERSION=$${OPERATOR_VERSION:-0.2.29}"
	@echo "  STRICT_RESULTS=true  # verify 时要求当前实例的新鲜 Result"
	@echo "  PURGE_SECRET=true    # uninstall 时同时删除 AI Secret"
	@echo "  PURGE_NAMESPACE=true # uninstall 时同时删除 Namespace"

preflight:
	@bash ./preflight.sh

platform-check:
	@python3 tests/platform-baseline-test.py

platform-smoke:
	@bash ./tests/kubernetes-v1.36-smoke.sh

bootstrap:
	@kubectl apply -f deploy/k8sgpt/namespace.yaml

bootstrap-secret: bootstrap
	@test -n "$${OPENAI_TOKEN:-}" || { echo "ERROR: OPENAI_TOKEN 未设置" >&2; exit 1; }
	@set -Eeuo pipefail; printf '%s' "$${OPENAI_TOKEN}" | kubectl create secret generic k8sgpt-openai-secret \
		-n k8sgpt-operator-system \
		--from-file=openai-api-key=/dev/stdin \
		--dry-run=client -o yaml | kubectl apply -f -

install:
	@bash ./install.sh

verify:
	@STRICT_RESULTS="$${STRICT_RESULTS:-false}" \
		RESULT_SINCE="$${RESULT_SINCE:-}" \
		EXPECTED_RESULT_KINDS="$${EXPECTED_RESULT_KINDS:-}" \
		REQUIRE_ANALYSIS_HEALTH="$${REQUIRE_ANALYSIS_HEALTH:-false}" \
		EXPECTED_ANALYSIS_INTERVAL="$${EXPECTED_ANALYSIS_INTERVAL:-5m}" \
		bash ./verify.sh

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
	@kubectl delete namespace k8sgpt-demo --ignore-not-found=true

status:
	@bash ./status.sh

results:
	@kubectl get results -n k8sgpt-operator-system -o wide

uninstall:
	@PURGE_SECRET="$${PURGE_SECRET:-false}" PURGE_NAMESPACE="$${PURGE_NAMESPACE:-false}" PURGE_DEMO="$${PURGE_DEMO:-false}" bash ./uninstall.sh

e2e:
	@set -a; source ./config/platform-versions.env; set +a; bash ./tests/e2e-kind.sh

e2e-provider:
	@set -a; source ./config/platform-versions.env; set +a; bash ./tests/e2e-provider-kind.sh

api-fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -d cmd internal; exit 1; }

api-test:
	@go test ./...

api-build:
	@mkdir -p "$(BUILD_DIR)"
	@go build -o "$(API_BINARY)" ./cmd/api

api-run:
	@go run ./cmd/api

api-contract-validate:
	@python tools/openapi/contract.py validate

api-client-generate:
	@python tools/openapi/contract.py generate --output clients/typescript/generated.ts

api-client-drift-check:
	@python tools/openapi/contract.py drift

api-contract-check:
	@python tools/openapi/contract.py check
