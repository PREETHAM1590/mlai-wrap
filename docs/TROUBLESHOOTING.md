# Troubleshooting

Each entry has the problem, its cause and the fix. Bridge messages appear in the terminal that runs
`mlai-wrap serve`, or in `~/mlai-bridge/wrap.log` when `claude` or `codex` started a private bridge.

## Many terminal tabs open on Windows

**Problem.** Windows Terminal opens and closes many tabs.

**Cause.** An old mlai-wrap (before 0.1.1) is still running, or plain `mlai-wrap` is an old npm
install. The old keeper used `mlai --bg`, which opens about 30 tabs each time.

**Fix.**

```powershell
Get-Process mlai-wrap, mlai-bridge -ErrorAction SilentlyContinue | Stop-Process
npm ls -g mlai-wrap
```

Then update to 0.1.1 or later, or build from source with `go build -o mlai-wrap.exe .`.

## UnicodeDecodeError: 'charmap' codec can't decode byte 0x9d

**Problem.** `mlai` fails with `UnicodeDecodeError: 'charmap' codec can't decode byte 0x9d`.

**Cause.** mlai's Windows engine reads its request as cp1252, and your Claude Code skills contain
characters such as U+201D.

**Fix.** Keep `MLAI_ISOLATE_SKILLS=true`, which is the default. See
[CONFIGURATION.md](CONFIGURATION.md#skill-isolation).

## Claude Code shows "API error · retrying" and the bridge log shows no requests

**Problem.** Claude Code retries and never reaches the bridge.

**Cause.** `ANTHROPIC_BASE_URL` is set in `~/.claude/settings.json`, and an `env` block there
overrides the process environment.

**Fix.** Version 0.1.1 passes the values with `--settings`, which beat the user's files. Update,
then check the command with:

```powershell
mlai-wrap claude --dry-run
```

## Requests are slow, or fall back to the CLI

**Problem.** Some requests take much longer than others, or the log says
`direct path failed (…) — falling back to CLI`.

**Cause.** Usually the run token: with mlai 0.3.27 a keeper run lasts about 40 s, and a request
that arrives after it ended waits for a new one (about 15 to 25 s), up to `-keeper-wait`.

**Fix.** Run `mlai-wrap status`. `keeper wait` shows the time spent on run tokens, `fallback`
the last reason, and `keeper` the failures in a row with the last error. Start the bridge with
`-prewarm` so the first request does not wait. A keeper that keeps failing is retried with a
growing pause (up to about 5 minutes); check `mlai whoami`. If a client prefers a fast error
over a slow CLI run, use `-direct-only` or the header `X-MLAI-Direct-Only: true`.

## "details: not available" in mlai-wrap status

**Cause.** The bridge is older than 0.1.1 (no `/v1/status`), or it has a key and `MLAI_API_KEY`
is not set in your shell.

**Fix.** Restart the bridge with the current version, or set `MLAI_API_KEY`.

## mlai-wrap claude starts a private bridge although serve is running

**Cause.** The `serve` bridge could not be verified: it is older than 0.1.1 (no discovery
file), it is not on a loopback address, or it rejects your `MLAI_API_KEY`.

**Fix.** Restart `serve` with the current version. Check that `~/mlai-bridge/serve.json` exists
while it runs. To use a specific bridge anyway, pass `--url`.

## Empty reply

**Problem.** The reply has no text.

**Cause.** A small `max_tokens`. The model's reasoning uses the tokens first.

**Fix.** Raise `max_tokens`, for example to 2000.

## "A Stop hook failed to execute: spawn EPERM"

**Problem.** This message appears as `mlai`'s answer.

**Cause.** It comes from mlai's own engine hook, not from the bridge.

**Fix.** None needed. The bridge returns the model's reply instead.

## Codex: "writing is blocked by read-only sandbox"

**Problem.** Codex cannot write files.

**Cause.** This is Codex's own Windows sandbox, not the bridge.

**Fix.** Change Codex's sandbox settings. See the Codex documentation.

## "codex not found in PATH" (exit 127)

**Problem.** `mlai-wrap codex` prints `codex not found in PATH` and exits with 127.

**Cause.** Codex is not installed, or its folder is not in `PATH`.

**Fix.**

```bash
npm install -g @openai/codex
```

`mlai-wrap doctor` shows whether `claude` and `codex` are found. The same applies to
`claude not found in PATH`.

## "a task can be up to 30,000" in the log

**Problem.** The log has a message that starts with `a task can be up to 30,000`.

**Cause.** The request fell back to the CLI, which accepts at most 30,000 characters. Claude Code
and Codex send larger requests.

**Fix.** Direct mode is needed for Claude Code and Codex. Make sure `MLAI_DIRECT` is not set to
`false`, and see [Requests are slow, or fall back to the CLI](#requests-are-slow-or-fall-back-to-the-cli).
