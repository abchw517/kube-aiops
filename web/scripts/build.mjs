import { spawnSync } from "node:child_process";
import { mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
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
    "--entry-names=portal-[hash]",
    "--minify",
  ],
  { cwd: webDir, stdio: "inherit" },
);

if (result.status !== 0) process.exit(result.status ?? 1);
const assets = await readdir(resolve(distDir, "assets"));
let html = await readFile(resolve(webDir, "index.html"), "utf8");
for (const extension of ["js", "css"]) {
  const asset = assets.find((name) => name.startsWith("portal-") && name.endsWith(`.${extension}`));
  if (!asset) throw new Error(`Missing Portal ${extension} asset`);
  html = html.replace(`/assets/portal.${extension}`, `/assets/${asset}`);
}
await writeFile(resolve(distDir, "index.html"), html);
