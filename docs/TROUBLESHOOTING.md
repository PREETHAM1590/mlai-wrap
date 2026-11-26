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

## Log says "direct mode is off"

**Problem.** The bridge log says direct mode is off, and requests use the slower CLI path.

**Cause.** A keeper run ended without a token. The bridge recorded this for the current mlai
version in `~/mlai-bridge/workspace/.keeper/unsupported`, and keeps direct mode off until
`mlai --version` changes.

**Fix.** Delete `~/mlai-bridge/workspace/.keeper/unsupported` to retry.

```powershell
Remove-Item "$HOME\mlai-bridge\workspace\.keeper\unsupported"
```

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

**Fix.** Direct mode is needed for Claude Code and Codex. Check for the entries above, in
particular "direct mode is off", and make sure `MLAI_DIRECT` is not set to `false`.
