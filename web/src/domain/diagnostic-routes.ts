export const diagnosticListPaths = {
  findings: "#/findings",
  overview: "#/diagnostics",
  events: "#/diagnostics/events",
} as const;

export type DiagnosticListView = keyof typeof diagnosticListPaths;

export function diagnosticListView(hash: string): DiagnosticListView {
  const path = hash.split("?", 1)[0] ?? "";
  if (path === diagnosticListPaths.overview) return "overview";
  if (path === diagnosticListPaths.events || path.startsWith(`${diagnosticListPaths.events}/`) || path.startsWith("#/diagnostics/incidents/")) return "events";
  return "findings";
}

export function diagnosticDetailLink(id: string, cluster: string, view: DiagnosticListView, returnQuery: string): string {
  const query = new URLSearchParams({ cluster, return: returnQuery });
  if (view === "overview") query.set("from", view);
  const listPath = diagnosticListPaths[view === "findings" ? "findings" : "events"];
  return `${listPath}/${encodeURIComponent(id)}?${query}`;
}
