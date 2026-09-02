import { spawnSync } from "node:child_process";
import { copyFile, mkdir, rm } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const distDir = resolve(webDir, "dist");

await rm(distDir, { recursive: true, force: true });
await mkdir(resolve(distDir, "assets"), { recursive: true });

const result = spawnSync(
  "npx",
  [
    "--yes",
    "esbuild@0.25.9",
    "src/main.ts",
    "--bundle",
    "--format=esm",
    "--target=es2022",
    "--outdir=dist/assets",
    "--entry-names=portal",
    "--minify",
  ],
  { cwd: webDir, stdio: "inherit" },
);

if (result.status !== 0) process.exit(result.status ?? 1);
await copyFile(resolve(webDir, "index.html"), resolve(distDir, "index.html"));
