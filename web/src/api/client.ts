import { KubeAIOpsApiClient } from "../../../clients/typescript/generated.ts";

function resolveApiBaseUrl(): string {
  const configured = document
    .querySelector<HTMLMetaElement>('meta[name="kube-aiops-api-base"]')
    ?.getAttribute("content")
    ?.trim();
  return configured ?? "";
}

export const apiClient = new KubeAIOpsApiClient({
  baseUrl: resolveApiBaseUrl(),
});
