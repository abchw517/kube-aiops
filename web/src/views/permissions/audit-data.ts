export const auditRoute = "#/admin/operations";
export const auditLevels = [["low", "低"], ["medium", "中"], ["high", "高"]] as const;
export const maxRetentionDays = 106_751_991_167_300;
const filterNames = ["account", "action", "risk", "batch", "before"] as const;

export type AuditFilters = Partial<Record<typeof filterNames[number], string>>;
export type AuditRetention = Record<typeof auditLevels[number][0], number>;
export type AuditOperation = {
  id: number; at: number; actor: string; action: string; risk: string; target: string;
  result: string; reason: string; batch: string;
};
export type AuditPage = {
  items: AuditOperation[];
  before: number;
  actions?: string[];
  retention?: AuditRetention;
};

/** 仅解析审计接口支持的筛选和游标字段。 */
export function readAuditFilters(fields: Pick<FormData, "get">): AuditFilters {
  const filters: AuditFilters = {};
  for (const name of filterNames) {
    const value = fields.get(name);
    if (typeof value !== "string" || !value.trim()) continue;
    if (name === "before" && !/^[1-9]\d*$/.test(value)) continue;
    if (name === "risk" && !auditLevels.some(([, risk]) => risk === value)) continue;
    filters[name] = value.trim();
  }
  return filters;
}

/** 仅识别固定的审计入口，参数作为数据参与编码。 */
export function parseAuditRoute(hash: string): AuditFilters | undefined {
  const [route, query] = hash.split(/\?(.*)/s, 2);
  return route === auditRoute ? readAuditFilters(new URLSearchParams(query)) : undefined;
}

/** 将有效字段编码为审计参数，省略空值。 */
export function auditQuery(filters: AuditFilters): string {
  const params = new URLSearchParams();
  for (const name of filterNames) {
    const value = filters[name];
    if (value) params.set(name, value);
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

/** 审计筛选可通过书签恢复，地址始终留在操作审计。 */
export function auditLocation(filters: AuditFilters): string {
  return `${auditRoute}${auditQuery(filters)}`;
}

/** 输入需与服务端相同，空值不能隐式转为 0。 */
export function readAuditRetention(fields: Pick<FormData, "get">): AuditRetention | undefined {
  const readDays = (key: keyof AuditRetention) => {
    const value = fields.get(key);
    return typeof value === "string" && value.trim() ? Number(value) : Number.NaN;
  };
  const values = { low: readDays("low"), medium: readDays("medium"), high: readDays("high") };
  if (Object.values(values).every((days) => Number.isSafeInteger(days) && days >= 0 && days <= maxRetentionDays)) return values;
}

/** 降到 0 会清除整级记录，只有发生这类变化时需要二次确认。 */
export function newlyDisabledAuditLevels(current: AuditRetention, next: AuditRetention): string[] {
  return auditLevels.filter(([key]) => current[key] > 0 && next[key] === 0).map(([, label]) => `${label}风险`);
}
