import { escapeHtml } from "./html.ts";

export function loadingState(label: string): string {
  return `
    <section class="state-card" role="status" aria-busy="true">
      <div class="spinner" aria-hidden="true"></div>
      <h2>正在加载…</h2>
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

/** 提供带独立标题和即时状态提示的登录表单。 */
export function authenticationRequiredState(message = ""): string {
  return `
    <div class="login-layout">
    <section class="login-overview" aria-labelledby="login-overview-heading">
      <h2 id="login-overview-heading">运维诊断控制台</h2>
      <p>在同一个工作区跟进告警、核对证据与查阅运维知识。</p>
      <ul class="login-capabilities">
        <li><strong>诊断总览</strong><span>跟进最近诊断与待处理事件</span></li>
        <li><strong>AI 调查</strong><span>按授权范围读取证据</span></li>
      </ul>
    </section>
    <section class="login-card" aria-labelledby="login-heading" data-testid="authentication-required-state">
      <h1 id="login-heading" tabindex="-1">登录控制台</h1>
      <p class="login-description">此工作区需要身份认证。可使用本地账号或 LDAP 账号登录。</p>
      ${message ? `<p>${escapeHtml(message)}</p>` : ""}
      <form class="login-form" data-login>
        <label>用户名<input name="username" autocomplete="username" spellcheck="false" required maxlength="256"></label>
        <label>密码<input name="password" type="password" autocomplete="current-password" required maxlength="1024"></label>
        <label class="login-provider"><input type="checkbox" name="provider" value="ldap"> LDAP / Active Directory 登录</label>
        <p role="status" data-login-status></p>
        <button class="button" type="submit">登录</button>
      </form>
    </section>
    </div>`;
}

export function authorizationDeniedState(): string {
  return `
    <section class="state-card error" role="alert" data-testid="authorization-denied-state">
      <h2>无访问权限</h2>
      <p>当前身份没有执行此操作或访问目标集群、命名空间的权限。</p>
    </section>`;
}

/** 认证配置加载失败时提供独立的重试入口。 */
export function authenticationConfigError(): string {
  return `<section class="state-card error" role="alert">
    <h2>无法加载认证配置</h2>
    <button class="button" type="button" data-action="retry">重试</button>
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
