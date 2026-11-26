// Shared launcher for the mlai-wrap and mlai-bridge commands. The Go binary
// comes from a per-platform package (optionalDependencies); nothing is
// downloaded or built at install time.
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { accessSync, chmodSync, constants } from "node:fs";

const PACKAGES = {
  "win32-x64": "mlai-wrap-win32-x64",
  "linux-x64": "mlai-wrap-linux-x64",
  "linux-arm64": "mlai-wrap-linux-arm64",
  "darwin-x64": "mlai-wrap-darwin-x64",
  "darwin-arm64": "mlai-wrap-darwin-arm64",
};

function fail(message, code = 1) {
  process.stderr.write(`${message}\n`);
  process.exit(code);
}

function resolveBinary() {
  const override = process.env.MLAI_WRAP_BINARY;
  if (override) return override;

  const key = `${process.platform}-${process.arch}`;
  const pkg = PACKAGES[key];
  if (!pkg) {
    fail(
      `mlai-wrap: no prebuilt binary for ${key}.\n` +
        `Supported: ${Object.keys(PACKAGES).join(", ")}.\n` +
        "Build from source (https://github.com/PREETHAM1590/mlai-wrap) and set MLAI_WRAP_BINARY to the binary.",
    );
  }
  const file = `${pkg}/bin/mlai-bridge${process.platform === "win32" ? ".exe" : ""}`;
  try {
    return createRequire(import.meta.url).resolve(file);
  } catch {
    fail(
      `mlai-wrap: the platform package ${pkg} is not installed.\n` +
        "It is an optional dependency of mlai-wrap. Reinstall without --omit=optional / --no-optional:\n" +
        "  npm install -g mlai-wrap\n" +
        "Or set MLAI_WRAP_BINARY to the path of an mlai-bridge binary.",
    );
  }
}

export function run(name) {
  const binary = resolveBinary();
  if (process.platform !== "win32") {
    // A package packed on a filesystem without permission bits loses +x.
    try {
      accessSync(binary, constants.X_OK);
    } catch {
      try {
        chmodSync(binary, 0o755);
      } catch {
        // spawn reports the real error below
      }
    }
  }
  const child = spawn(binary, process.argv.slice(2), {
    argv0: name, // ignored on Windows, so the name is also passed in the environment
    env: { ...process.env, MLAI_WRAP_ARGV0: name },
    stdio: "inherit",
    windowsHide: false,
    shell: false,
  });

  // SIGINT reaches the child from the terminal. Keep this process alive until
  // the child exits instead of dying first and leaving the child running.
  const onSigint = () => {};
  const forward = (signal) => () => {
    try {
      child.kill(signal);
    } catch {
      // the child is already gone
    }
  };
  const handlers = { SIGINT: onSigint, SIGTERM: forward("SIGTERM"), SIGHUP: forward("SIGHUP") };
  const removeHandlers = () => {
    for (const [signal, handler] of Object.entries(handlers)) process.removeListener(signal, handler);
  };
  for (const [signal, handler] of Object.entries(handlers)) process.on(signal, handler);

  child.on("error", (err) => {
    removeHandlers();
    if (err.code === "ENOENT") {
      fail(`${name}: cannot start ${binary}: file not found`, 127);
    }
    fail(`${name}: cannot start ${binary}: ${err.message}`, 1);
  });

  child.on("exit", (code, signal) => {
    removeHandlers();
    if (signal && process.platform !== "win32") {
      process.kill(process.pid, signal);
      // Only reached when the signal did not end this process.
      process.exit(1);
    }
    process.exit(code ?? 1);
  });
}
