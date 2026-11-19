import type { Severity } from "../../api/generated.ts";
import { formatSeverity } from "../../components/html.ts";

const descriptions: Record<Severity, string> = {
  critical: "关键服务已中断或主要功能受明显影响，应优先排查。例如关键工作负载无可用副本。",
  warning: "服务仍可提供，但出现降级或存在故障风险，应安排排查。例如副本数下降或工作负载反复重启。",
  info: "可关注的现象或优化建议，暂未观察到直接服务影响。例如资源配置优化建议。",
};

/** 将级别名称和影响范围关联到同一个说明入口。 */
export function renderSeverityHelp(severity: Severity): string {
  const id = `severity-description-${severity}`;
  return `<div class="severity-help">
    <button type="button" class="severity-help-trigger" aria-expanded="false"
      aria-controls="${id}" aria-describedby="${id}">${formatSeverity(severity)} <span aria-hidden="true">?</span></button>
    <p id="${id}" class="severity-help-description" hidden>${descriptions[severity]}</p>
  </div>`;
}

/** 支持悬停、聚焦和点按，并让说明区域在鼠标移入时保持可见。 */
export function bindSeverityHelp(root: HTMLElement): void {
  for (const help of root.querySelectorAll<HTMLElement>(".severity-help")) {
    const trigger = help.querySelector<HTMLButtonElement>("button");
    const description = help.querySelector<HTMLElement>(".severity-help-description");
    if (!trigger || !description) continue;
    let pinned = false;
    const show = () => {
      description.hidden = false;
      trigger.setAttribute("aria-expanded", "true");
    };
    const close = () => {
      pinned = false;
      description.hidden = true;
      trigger.setAttribute("aria-expanded", "false");
    };
    help.addEventListener("pointerenter", (event) => {
      if (event.pointerType === "mouse") show();
    });
    help.addEventListener("pointerleave", () => {
      if (!pinned && document.activeElement !== trigger) close();
    });
    trigger.addEventListener("focus", show);
    trigger.addEventListener("blur", () => {
      if (!pinned && !help.matches(":hover")) close();
    });
    trigger.addEventListener("click", () => {
      if (pinned) close();
      else {
        pinned = true;
        show();
      }
    });
    help.addEventListener("dismiss-severity-help", close);
  }
}

/** Escape 关闭所有已打开的说明并保留当前焦点。 */
export function dismissSeverityHelp(event: KeyboardEvent, root: HTMLElement): void {
  if (event.key !== "Escape") return;
  for (const opened of root.querySelectorAll('.severity-help-trigger[aria-expanded="true"]')) {
    opened.parentElement?.dispatchEvent(new Event("dismiss-severity-help"));
  }
}
