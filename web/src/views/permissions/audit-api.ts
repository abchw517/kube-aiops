import { ApiError } from "../../api/generated.ts";
import { auditQuery, defaultAuditLimit, readAuditLimit, type AuditFilters, type AuditListPage, type AuditPage, type AuditRetention } from "./audit-data.ts";

const operationsPath = "/api/v1/admin/operations";

/** 查询保留期内的事件，保留 HTTP 状态供会话恢复。 */
export async function fetchAuditPage(filters: AuditFilters): Promise<AuditPage> {
  const response = await fetch(`${operationsPath}${auditQuery(filters)}`);
  if (!response.ok) throw new ApiError(response.status, null);
  return await response.json() as AuditPage;
}

/** 请求服务端计算的筛选总数、实际页码和当前页记录。 */
export async function fetchAuditList(filters: AuditFilters): Promise<AuditListPage> {
  const data = await fetchAuditPage({ ...filters, page: filters.page ?? 1, limit: filters.limit ?? defaultAuditLimit });
  if (!("page" in data && typeof data.page === "number" && Number.isSafeInteger(data.page) && data.page > 0 &&
        "total" in data && typeof data.total === "number" && Number.isSafeInteger(data.total) && data.total >= 0 &&
        "limit" in data && typeof data.limit === "number" && readAuditLimit(String(data.limit)))) {
    throw new Error("Invalid audit pagination response");
  }
  return data as AuditListPage;
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
