import type { Severity } from "../../../clients/typescript/generated.ts";

const severityLabels: Record<Severity, string> = {
  critical: "严重",
  warning: "警告",
  info: "信息",
};

export function formatSeverity(severity: Severity): string {
  return severityLabels[severity] ?? severity;
}

export function escapeHtml(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

export function formatTimestamp(value?: string): string {
  if (!value) return "—";
  const parsed = new Date(value);
  return Number.isNaN(parsed.valueOf()) ? escapeHtml(value) : parsed.toLocaleString("zh-CN");
}
