import { ApiError } from "../../api/generated.ts";

export function sourceError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return "会话已过期，请重新登录。";
    if (error.status === 403) return "当前账号无权操作此数据源或资源范围。";
    if (error.status === 404) return "数据源或相关服务暂不可用。";
    if (error.status === 412) return "配置已更新，请核对最新配置后重试。";
    const message = (error.body as { error?: { message?: unknown } } | undefined)?.error?.message;
    if (typeof message === "string" && message.length < 300) return message;
  }
  return "数据源操作未完成，请稍后重试。";
}
