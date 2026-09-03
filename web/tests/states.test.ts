import assert from "node:assert/strict";
import test from "node:test";
import {
  authenticationRequiredState,
  authorizationDeniedState,
  emptyState,
  errorState,
  loadingState,
} from "../src/components/states.ts";

test("renders explicit loading and empty states", () => {
  assert.match(loadingState("Loading findings"), /aria-busy="true"/);
  assert.match(emptyState(), /data-testid="empty-state"/);
});

test("error state always exposes a retry control", () => {
  const rendered = errorState("backend unavailable");
  assert.match(rendered, /data-testid="error-state"/);
  assert.match(rendered, /data-action="retry"/);
});

test("authentication required state is explicit and retryable", () => {
  const rendered = authenticationRequiredState();
  assert.match(rendered, /data-testid="authentication-required-state"/);
  assert.match(rendered, /Authentication required/);
  assert.match(rendered, /data-action="retry"/);
});

test("authorization denied state is explicit and does not imply frontend authorization", () => {
  const rendered = authorizationDeniedState();
  assert.match(rendered, /data-testid="authorization-denied-state"/);
  assert.match(rendered, /Access denied/);
  assert.doesNotMatch(rendered, /data-action="retry"/);
});
