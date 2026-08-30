#!/usr/bin/env bash
set -Eeuo pipefail

tool="$(basename "$0")"
mode="${MOCK_OBSERVABILITY_MODE:-healthy}"

if [[ "$tool" == "helm" ]]; then
  if [[ "${1:-}" == "status" ]]; then
    printf '%s\n' '{"version":3,"info":{"status":"deployed"}}'
    exit 0
  fi
  echo "unexpected helm invocation: $*" >&2
  exit 99
fi

[[ "$tool" == "kubectl" ]] || { echo "unexpected tool: $tool" >&2; exit 99; }

if [[ "${1:-}" == "config" && "${2:-}" == "current-context" ]]; then
  echo mock-context
  exit 0
fi
if [[ "${1:-}" == "version" ]]; then
  if [[ "$mode" == "api_timeout" ]]; then
    echo 'Unable to connect to the server: context deadline exceeded' >&2
    exit 1
  fi
  echo 'Client Version: v1.34.8'
  exit 0
fi
if [[ "${1:-}" == "auth" && "${2:-}" == "can-i" ]]; then
  if [[ " $* " == *" --subresource=log "* ]]; then
    echo no
    exit 1
  fi
  case " $* " in
    *" get pods "*|*" list deployments.apps "*|*" list persistentvolumeclaims "*)
      echo yes
      exit 0
      ;;
    *)
      echo no
      exit 1
      ;;
  esac
fi

if [[ "${1:-}" != "get" ]]; then
  echo "unexpected kubectl invocation: $*" >&2
  exit 99
fi

resource="${2:-}"
name="${3:-}"
if [[ "$resource" == "namespace" && "$name" == "k8sgpt-operator-system" ]]; then
  case "$mode" in
    namespace_absent) exit 0 ;;
    forbidden_namespace)
      echo 'Error from server (Forbidden): namespaces "k8sgpt-operator-system" is forbidden' >&2
      exit 1
      ;;
    timeout_namespace)
      echo 'Unable to connect to the server: context deadline exceeded' >&2
      exit 1
      ;;
  esac
  printf '%s\n' '{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"k8sgpt-operator-system"}}'
  exit 0
fi

if [[ "$resource" == "crd" ]]; then
  printf '{"apiVersion":"apiextensions.k8s.io/v1","kind":"CustomResourceDefinition","metadata":{"name":"%s"}}\n' "$name"
  exit 0
fi
if [[ "$resource" == "serviceaccount" ]]; then
  printf '%s\n' '{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"k8sgpt"}}'
  exit 0
fi
if [[ "$resource" == "networkpolicy" ]]; then
  printf '%s\n' '{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"kube-aiops-egress-baseline"}}'
  exit 0
fi
if [[ "$resource" == "secret" ]]; then
  if [[ " $* " == *" -o go-template="* ]]; then
    printf 'present'
  else
    printf '%s\n' '{"apiVersion":"v1","kind":"Secret","data":{"openai-api-key":"dGVzdA=="}}'
  fi
  exit 0
fi
if [[ "$resource" == "mutations" ]]; then
  printf '%s\n' '{"items":[]}'
  exit 0
fi
if [[ "$resource" == "results" ]]; then
  if [[ "$mode" == "forbidden_results" ]]; then
    echo 'Error from server (Forbidden): results.core.k8sgpt.ai is forbidden' >&2
      exit 1
  fi
  if [[ "$mode" == "malformed_results" ]]; then
    printf '%s\n' '{"items":{}}'
    exit 0
  fi
  printf '%s\n' '{"items":[]}'
  exit 0
fi
if [[ "$resource" == "lease" ]]; then
  exit 0
fi
if [[ "$resource" == "k8sgpt" ]]; then
  if [[ -n "${MOCK_K8SGPT_COUNT_FILE:-}" ]]; then
    count=0
    [[ ! -f "$MOCK_K8SGPT_COUNT_FILE" ]] || count="$(<"$MOCK_K8SGPT_COUNT_FILE")"
    count=$((count + 1))
    printf '%s\n' "$count" >"$MOCK_K8SGPT_COUNT_FILE"
    if ((count > 1)); then
      echo 'K8sGPT CR queried more than once' >&2
      exit 88
    fi
  fi
  printf '%s\n' '{"apiVersion":"core.k8sgpt.ai/v1alpha1","kind":"K8sGPT","metadata":{"name":"k8sgpt-engine"},"spec":{"ai":{"enabled":true,"anonymized":true,"backend":"openai","secret":{"name":"k8sgpt-openai-secret","key":"openai-api-key"},"autoRemediation":{"enabled":false}},"analysis":{"interval":"5m"},"filters":["Pod","Deployment","PersistentVolumeClaim"]},"status":{"lastAnalysisError":""}}'
  exit 0
fi
if [[ "$resource" == "deployment" && " $* " == *" -l app.kubernetes.io/name=k8sgpt-operator "* ]]; then
  if [[ "$mode" == "operator_partial" ]]; then
    printf '%s\n' '{"items":[{"metadata":{"name":"k8sgpt-operator","generation":2},"spec":{"replicas":2},"status":{"observedGeneration":2,"readyReplicas":1,"availableReplicas":1,"unavailableReplicas":1}}]}'
  else
    printf '%s\n' '{"items":[{"metadata":{"name":"k8sgpt-operator","generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"readyReplicas":1,"availableReplicas":1,"unavailableReplicas":0}}]}'
  fi
  exit 0
fi
if [[ "$resource" == "deployment" && "$name" == "k8sgpt-engine" ]]; then
  printf '%s\n' '{"metadata":{"name":"k8sgpt-engine","generation":3},"spec":{"replicas":1},"status":{"observedGeneration":3,"readyReplicas":1,"availableReplicas":1,"unavailableReplicas":0}}'
  exit 0
fi

echo "unexpected kubectl get invocation: $*" >&2
exit 99
