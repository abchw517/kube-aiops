import { KubeAIOpsApiClient } from "./generated.ts";

function resolveApiBaseUrl(): string {
  const configured = document
    .querySelector<HTMLMetaElement>('meta[name="kube-aiops-api-base"]')
    ?.getAttribute("content")
    ?.trim();
  return configured ?? "";
}

export const apiClient = new KubeAIOpsApiClient({
  baseUrl: resolveApiBaseUrl(),
  fetch: globalThis.fetch.bind(globalThis),
});

/** Bind requests for an overview navigation to its shared deadline. */
export function createOverviewClient(signal: AbortSignal): KubeAIOpsApiClient {
  return new KubeAIOpsApiClient({
    baseUrl: resolveApiBaseUrl(),
    fetch: (input, init) => globalThis.fetch(input, { ...init, signal }),
  });
}
