import type {
  ListFindingsParams,
  Severity,
  SummarizeFindingsParams,
} from "../../../clients/typescript/generated.ts";

export type FindingFilters = Pick<
  ListFindingsParams,
  "cluster" | "namespace" | "kind" | "severity"
>;

const severityValues = new Set<Severity>(["critical", "warning", "info"]);

export function parseFindingFilters(hash: string): FindingFilters {
  const queryStart = hash.indexOf("?");
  const query = queryStart >= 0 ? hash.slice(queryStart + 1) : "";
  const params = new URLSearchParams(query);
  const severity = params.get("severity");

  return compactFilters({
    cluster: params.get("cluster") === "local" ? "local" : "local",
    namespace: cleanValue(params.get("namespace")),
    kind: cleanValue(params.get("kind")),
    severity:
      severity !== null && severityValues.has(severity as Severity)
        ? (severity as Severity)
        : undefined,
  });
}

export function serializeFindingFilters(filters: FindingFilters): string {
  const params = new URLSearchParams();
  if (filters.cluster) params.set("cluster", filters.cluster);
  if (filters.namespace) params.set("namespace", filters.namespace);
  if (filters.kind) params.set("kind", filters.kind);
  if (filters.severity) params.set("severity", filters.severity);
  return params.toString();
}

export function toListParams(filters: FindingFilters): ListFindingsParams {
  return { ...compactFilters(filters), limit: 50 };
}

export function toSummaryParams(
  filters: FindingFilters,
): SummarizeFindingsParams {
  return compactFilters(filters);
}

function compactFilters(filters: FindingFilters): FindingFilters {
  return {
    cluster: filters.cluster ?? "local",
    namespace: cleanValue(filters.namespace),
    kind: cleanValue(filters.kind),
    severity: filters.severity,
  };
}

function cleanValue(value: string | null | undefined): string | undefined {
  const cleaned = value?.trim();
  return cleaned ? cleaned : undefined;
}
