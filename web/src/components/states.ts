import { escapeHtml } from "./html.ts";

export function loadingState(label: string): string {
  return `
    <section class="state-card" role="status" aria-busy="true">
      <div class="spinner" aria-hidden="true"></div>
      <h2>Loading</h2>
      <p>${escapeHtml(label)}</p>
    </section>`;
}

export function emptyState(): string {
  return `
    <section class="state-card" data-testid="empty-state">
      <h2>No findings</h2>
      <p>No findings match the current cluster, namespace, severity, and kind filters.</p>
    </section>`;
}

export function authenticationRequiredState(): string {
  return `
    <section class="state-card error" role="alert" data-testid="authentication-required-state">
      <h2>Authentication required</h2>
      <p>Your session or trusted identity is required before this read-only Portal can load data.</p>
      <button class="button" type="button" data-action="retry">Retry</button>
    </section>`;
}

export function authorizationDeniedState(): string {
  return `
    <section class="state-card error" role="alert" data-testid="authorization-denied-state">
      <h2>Access denied</h2>
      <p>Your authenticated identity is not authorized for this capability or cluster/namespace scope.</p>
    </section>`;
}

export function errorState(message: string): string {
  return `
    <section class="state-card error" role="alert" data-testid="error-state">
      <h2>Unable to load findings</h2>
      <p>${escapeHtml(message)}</p>
      <button class="button" type="button" data-action="retry">Retry</button>
    </section>`;
}
