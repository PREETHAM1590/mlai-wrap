#!/usr/bin/env node
// Usage: node scripts/npm-build.mjs <version> <distdir>
//
// Assembles the npm packages under dist/npm from the Go binaries in <distdir>
// (named mlai-bridge_<goos>_<goarch>[.exe]). Prints the package directories
// in publish order, platform packages first, one per line. Diagnostics go to
// stderr so stdout can be read by a script.
import {
  chmodSync,
  copyFileSync,
  cpSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const SEMVER =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$/;

function die(message) {
  process.stderr.write(`npm-build: ${message}\n`);
  process.exit(1);
}

const [version, distArg, ...extra] = process.argv.slice(2);
if (!version || !distArg || extra.length) die("usage: node scripts/npm-build.mjs <version> <distdir>");
if (!SEMVER.test(version)) die(`"${version}" is not a semantic version (no leading "v", e.g. 1.2.3)`);

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const distDir = path.resolve(distArg);
const outDir = path.join(root, "dist", "npm");
const readJSON = (file) => JSON.parse(readFileSync(file, "utf8"));

const platforms = readJSON(path.join(root, "npm", "platform.json"));
const mainSrc = path.join(root, "npm", "mlai-wrap");
const main = readJSON(path.join(mainSrc, "package.json"));

// Check every input before writing anything.
const inputs = platforms.map((p) => ({ ...p, src: path.join(distDir, `mlai-bridge_${p.goos}_${p.goarch}${p.exe}`) }));
for (const p of inputs) if (!existsSync(p.src)) die(`missing binary ${p.src}`);
for (const file of ["README.md", "LICENSE"]) {
  if (!existsSync(path.join(root, file))) die(`missing ${file} in the repository root`);
}

rmSync(outDir, { recursive: true, force: true });
const dirs = [];

for (const p of inputs) {
  const name = p.pkg.split("/").pop();
  const dir = path.join(outDir, name);
  mkdirSync(path.join(dir, "bin"), { recursive: true });
  const manifest = {
    name: p.pkg,
    version,
    description: `${p.goos}/${p.goarch} binary for mlai-wrap`,
    os: [p.os],
    cpu: [p.cpu],
    license: main.license,
    repository: main.repository,
    homepage: main.homepage,
    files: ["bin"],
    preferUnplugged: true,
  };
  writeFileSync(path.join(dir, "package.json"), `${JSON.stringify(manifest, null, 2)}\n`);
  const target = path.join(dir, "bin", `mlai-bridge${p.exe}`);
  copyFileSync(p.src, target);
  chmodSync(target, 0o755);
  dirs.push(dir);
}

const mainDir = path.join(outDir, "mlai-wrap");
cpSync(mainSrc, mainDir, { recursive: true });
const manifest = readJSON(path.join(mainDir, "package.json"));
manifest.version = version;
manifest.optionalDependencies = Object.fromEntries(platforms.map((p) => [p.pkg, version]));
writeFileSync(path.join(mainDir, "package.json"), `${JSON.stringify(manifest, null, 2)}\n`);
for (const file of ["README.md", "LICENSE"]) copyFileSync(path.join(root, file), path.join(mainDir, file));
dirs.push(mainDir);

process.stdout.write(`${dirs.join("\n")}\n`);
