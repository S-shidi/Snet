import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync } from "node:fs";
import { dirname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..");
const dist = join(here, "dist");
const webBuild = join(root, "deploy", "docker", "web-build");
const npm = process.platform === "win32" ? "npm.cmd" : "npm";
const npx = process.platform === "win32" ? "npx.cmd" : "npx";

mkdirSync(dist, { recursive: true });
copyFileSync(join(root, "shared", "web", "index.html"), join(dist, "index.html"));
copyFileSync(join(root, "shared", "web", "styles.css"), join(dist, "styles.css"));
copyFileSync(join(root, "shared", "web", "snet-logo.svg"), join(dist, "snet-logo.svg"));

if (!existsSync(join(webBuild, "node_modules"))) {
  execFileSync(npm, ["ci", "--omit=dev"], { cwd: webBuild, stdio: "inherit", shell: process.platform === "win32" });
}

const outfile = relative(root, join(dist, "app.js")).split(sep).join("/");
execFileSync(
  npx,
  [
    "--yes",
    "esbuild@0.25.12",
    "deploy/docker/web-build/web.ts",
    "--bundle",
    "--format=iife",
    "--target=es2022",
    "--minify",
    `--outfile=${outfile}`,
  ],
  {
    cwd: root,
    stdio: "inherit",
    shell: process.platform === "win32",
    env: { ...process.env, NODE_PATH: join(webBuild, "node_modules") },
  },
);
