# Commands

`mlai-wrap` and `mlai-bridge` are the same binary. `make install` links `mlai-wrap` to
`mlai-bridge`; on Windows copy `mlai-bridge.exe` to `mlai-wrap.exe`. The npm package installs
both names.

- [serve](#serve)
- [claude and codex](#claude-and-codex)
- [env](#env)
- [models](#models)
- [status](#status)
- [doctor](#doctor)
- [version and help](#version-and-help)
- [npm launcher](#npm-launcher)

## serve

```
mlai-wrap [flags]
mlai-wrap serve [flags]
```

Runs the bridge. With no arguments, a binary named `mlai-wrap` prints the help text instead.

| Option | Env var | Default | Meaning |
| --- | --- | --- | --- |
| `-port` | `MLAI_PORT` | `8197` | listen port |
| `-bind` | `MLAI_BIND` | `127.0.0.1` | listen address; a non-loopback address requires a key |
| `-api-key` | `MLAI_API_KEY` | empty | require this bearer key on `/v1/*`; prefer the env var, because flags show in process lists |
| `-mlai` | `MLAI_BIN` | `mlai` | path to the mlai CLI |
| `-workdir` | `MLAI_WORKDIR` | `~/mlai-bridge/workspace` | folder the agent runs in |
| `-model` | `MLAI_MODEL` | `pixis-opus` | default model id |
| `-concurrency` | `MLAI_CONCURRENCY` | `1` | max parallel CLI runs |
| `-direct-concurrency` | `MLAI_DIRECT_CONCURRENCY` | `8` | max parallel gateway requests |
| `-queue-wait` | none | `30s` | how long a request waits for a free slot |
| `-timeout` | none | `10m` | per-request wall clock limit |
| `-cache-ttl` | none | `10m` | identical-request cache TTL (`0` disables) |
| `-cache-size` | none | `512` | cache entries |
| `-effort` | `MLAI_EFFORT` | empty | reasoning effort: `low`, `medium`, `high` or `max` |
| `-direct` | `MLAI_DIRECT` | `true` | talk to the gateway directly (real token streaming) with CLI fallback |
| `-isolate-skills` | `MLAI_ISOLATE_SKILLS` | `true` | run `mlai` without your Claude Code skills and agents |

The `-api-key` flag overrides `MLAI_API_KEY`. Without a key the bridge refuses to start on a
non-loopback address. See [CONFIGURATION.md](CONFIGURATION.md) and
[DEPLOYMENT.md](DEPLOYMENT.md).

Exit codes: the bridge runs until it is stopped. Invalid flags make it exit with the standard Go
flag error status.

Examples:

```bash
mlai-wrap serve
mlai-wrap serve -port 8197 -model pixis-opus -concurrency 2 -cache-ttl 15m
MLAI_API_KEY="$(cat .apikey)" mlai-wrap serve -bind 0.0.0.0
```

```powershell
mlai-wrap serve
$env:MLAI_API_KEY = '<key>'; mlai-wrap serve -port 8200
```

## claude and codex

```
mlai-wrap claude [opts] [--] [args...]
mlai-wrap codex  [opts] [--] [args...]
```

Starts Claude Code or Codex against a bridge. Everything after the first non-option argument, or
after `--`, is passed to the client unchanged.

| Option | Meaning |
| --- | --- |
| `-m`, `--model ID` | model id for the client; default is `MLAI_MODEL`, then `pixis-opus` |
| `--url URL` | use the bridge at this URL |
| `--dry-run` | print the command, the variable names (key shown as `***`) and which bridge would be used; nothing starts |

### Which bridge is used

In this order:

1. The bridge at `--url` or `MLAI_BRIDGE_URL`.
2. A bridge already answering on `127.0.0.1:8197`. The key comes from `MLAI_API_KEY`. A bridge
   that rejects the key is skipped.
3. A private bridge on `127.0.0.1` with a random key. It is stopped when the client exits. Its
   log is `~/mlai-bridge/wrap.log`.

Plain `http` to a non-loopback host is refused. Use `https`.

No Claude Code or Codex file is written. The launcher sets variables and command-line options for
the client process only. `127.0.0.1,localhost,::1` is added to `NO_PROXY`, so the key never goes
through a proxy.

### claude

Variables set:

- `ANTHROPIC_BASE_URL`
- `ANTHROPIC_AUTH_TOKEN` (the bridge key)
- `ANTHROPIC_MODEL`
- `ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL`,
  `ANTHROPIC_DEFAULT_HAIKU_MODEL`, `ANTHROPIC_DEFAULT_FABLE_MODEL`
- `ANTHROPIC_SMALL_FAST_MODEL`
- `CLAUDE_CODE_SUBAGENT_MODEL`
- `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1`
- `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`

Variables removed: `ANTHROPIC_API_KEY`, `ANTHROPIC_BETAS`, `ANTHROPIC_CUSTOM_HEADERS`,
`CLAUDE_CODE_OAUTH_TOKEN`, `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`,
`CLAUDE_CODE_USE_FOUNDRY`, `CLAUDE_CODE_USE_MANTLE`, `MLAI_API_KEY`.

The launcher also passes the same values, never the key, with `--settings`. An `env` block in
`~/.claude/settings.json` would otherwise override the process environment and send Claude Code
to another endpoint.

### codex

Codex gets these command-line options:

- `-c model_provider='mlai'`
- `-c model_providers.mlai.name`, `model_providers.mlai.base_url` (`<bridge>/v1`),
  `model_providers.mlai.env_key` (`MLAI_BRIDGE_KEY`), `model_providers.mlai.wire_api`
  (`responses`) and `model_providers.mlai.supports_websockets=false`
- `-m <model>`

The key is passed in `MLAI_BRIDGE_KEY`. Variables removed: `OPENAI_API_KEY`, `OPENAI_BASE_URL`,
`MLAI_API_KEY`.

### Exit codes

| Code | Meaning |
| --- | --- |
| the client's own code | the client ran and exited |
| `1` | the bridge at `--url` is not healthy, or the private bridge could not start |
| `2` | bad arguments, an invalid bridge URL, or a `.cmd` shim argument that `cmd.exe` would misread |
| `126` | the client could not start |
| `127` | the client was not found in `PATH` |
| `128+n` | the client was killed by signal `n` |

### Examples

```bash
mlai-wrap claude -m pixis -- -p "fix the failing test"
mlai-wrap codex -- exec "explain this repo"
mlai-wrap claude --dry-run
MLAI_API_KEY="$(cat .apikey)" mlai-wrap claude --url https://your.host/mlai
```

```powershell
mlai-wrap claude -m pixis -- -p "fix the failing test"
mlai-wrap codex -m pixis-sol
$env:MLAI_API_KEY = '<key>'; mlai-wrap claude --url https://your.host/mlai
```

## env

```
mlai-wrap env <claude|codex> [--url URL] [--powershell] [-m ID]
```

Prints `export` lines (or `$env:` lines with `--powershell`) for a running bridge, so you can
start the client yourself. The key is referenced as `MLAI_API_KEY` and never printed. The output
also has unset lines (or `Remove-Item Env:` lines) for the variables the launcher removes, and a
final comment with the extra client arguments to use.

| Option | Meaning |
| --- | --- |
| `--url URL` | bridge URL; default `MLAI_BRIDGE_URL`, then `http://127.0.0.1:8197` |
| `--powershell` | print `$env:` lines |
| `-m ID` | model id; default `MLAI_MODEL`, then `pixis-opus` |

Exit codes: `0` on success, `2` for bad arguments or an invalid URL.

```bash
eval "$(mlai-wrap env claude)"
```

```powershell
mlai-wrap env codex --powershell | Invoke-Expression
```

## models

```
mlai-wrap models [--url URL]
```

Prints the model ids of a running bridge, one per line. The key comes from `MLAI_API_KEY`.
Exit codes: `0`, `1` when the request fails, `2` for bad arguments.

```bash
mlai-wrap models
```

## status

```
mlai-wrap status [--url URL]
```

Prints a one-line health summary: version, URL, status, whether `mlai` is signed in, and uptime.
Exit code `1` when no healthy bridge answers, `2` for bad arguments.

```bash
mlai-wrap status --url http://127.0.0.1:8197
```

## doctor

```
mlai-wrap doctor
```

Checks that `mlai`, `claude` and `codex` are on `PATH` and that `mlai` is signed in. It prints
`yes` or `no` for each. Exit code `1` if `mlai` is missing or signed out, otherwise `0`. A
missing `claude` or `codex` does not change the exit code.

## version and help

`mlai-wrap version` prints the version. `mlai-wrap help` prints the usage text. An unknown
command prints the usage text to stderr and exits with `2`.

## npm launcher

The npm package starts the binary from the platform package `@mlai-wrap/<os>-<cpu>`.

| Env var | Meaning |
| --- | --- |
| `MLAI_WRAP_BINARY` | run this binary instead of the platform package |

```powershell
$env:MLAI_WRAP_BINARY = 'C:\path\to\mlai-wrap.exe'
```
