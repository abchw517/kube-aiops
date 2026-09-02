import assert from "node:assert/strict";
import test from "node:test";
import { emptyState, errorState, loadingState } from "../src/components/states.ts";

test("renders explicit loading and empty states", () => {
  assert.match(loadingState("Loading findings"), /aria-busy="true"/);
  assert.match(emptyState(), /data-testid="empty-state"/);
});

test("error state always exposes a retry control", () => {
  const rendered = errorState("backend unavailable");
  assert.match(rendered, /data-testid="error-state"/);
  assert.match(rendered, /data-action="retry"/);
});
