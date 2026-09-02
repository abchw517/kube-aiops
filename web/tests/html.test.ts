import assert from "node:assert/strict";
import test from "node:test";
import { escapeHtml } from "../src/components/html.ts";

test("escapes backend-controlled text before HTML rendering", () => {
  assert.equal(
    escapeHtml('<img src=x onerror="alert(1)">'),
    "&lt;img src=x onerror=&quot;alert(1)&quot;&gt;",
  );
});
