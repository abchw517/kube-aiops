import { escapeHtml } from "./html.ts";

export function loadingState(label: string): string {
  return `
    <section class="state-card" role="status" aria-busy="true">
      <div class="spinner" aria-hidden="true"></div>
      <h2>正在加载</h2>
      <p>${escapeHtml(label)}</p>
    </section>`;
}

export function emptyState(): string {
  return `
    <section class="state-card" data-testid="empty-state">
      <h2>暂无诊断结果</h2>
      <p>当前筛选条件下没有匹配的诊断结果。</p>
    </section>`;
}

export function authenticationRequiredState(): string {
  return `
    <section class="state-card error" role="alert" data-testid="authentication-required-state">
      <h2>需要身份认证</h2>
      <p>请登录或恢复受信任的会话后重试。</p>
      <button class="button" type="button" data-action="retry">重试</button>
    </section>`;
}

export function authorizationDeniedState(): string {
  return `
    <section class="state-card error" role="alert" data-testid="authorization-denied-state">
      <h2>无访问权限</h2>
      <p>当前身份没有执行此操作或访问目标集群、命名空间的权限。</p>
    </section>`;
}

export function errorState(message: string): string {
  return `
    <section class="state-card error" role="alert" data-testid="error-state">
      <h2>加载诊断结果失败</h2>
      <p>${escapeHtml(message)}</p>
      <button class="button" type="button" data-action="retry">重试</button>
    </section>`;
}
