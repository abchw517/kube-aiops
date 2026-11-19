import { ApiError } from "../../api/generated.ts";
import { auditQuery, type AuditFilters, type AuditPage, type AuditRetention } from "./audit-data.ts";

const operationsPath = "/api/v1/admin/operations";

/** 查询保留期内的事件，保留 HTTP 状态供会话恢复。 */
export async function fetchAuditPage(filters: AuditFilters): Promise<AuditPage> {
  const response = await fetch(`${operationsPath}${auditQuery(filters)}`);
  if (!response.ok) throw new ApiError(response.status, null);
  return await response.json() as AuditPage;
}

/** 保存失败保留编辑草稿，响应正文仅作为提示文本使用。 */
export async function saveAuditRetention(retention: AuditRetention): Promise<void> {
  const response = await fetch(operationsPath, {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(retention),
  });
  if (response.status === 204) return;
  const message = response.headers.get("Content-Type")?.startsWith("text/plain") ? (await response.text()).trim() : null;
  throw new ApiError(response.status, message);
}
