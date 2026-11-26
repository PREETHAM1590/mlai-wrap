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
| `-bind` | `MLAI_BIND` | `127.0.0.1` | listen address; a non-loopback address requires a key and `-allow-plain-http` |
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
| `-direct-only` | `MLAI_DIRECT_ONLY` | `false` | never fall back to the CLI: when the gateway path fails, answer `503` with the reason. Clients can ask for this per request with the header `X-MLAI-Direct-Only: true` |
| `-prewarm` | `MLAI_PREWARM` | `false` | start the keeper when the bridge starts, so the first request finds a run token (costs one ML AI Code run) |
| `-keeper-wait` | `MLAI_KEEPER_WAIT` | `30s` | how long a request waits for a run token before it falls back (or fails, with direct-only); the keeper run goes on for the next request; `0` = the whole `-timeout` |
| `-allow-plain-http` | `MLAI_ALLOW_PLAIN_HTTP` | `false` | allow a non-loopback `-bind`, which sends the key and request bodies over plain HTTP; trusted networks only |

The `-api-key` flag overrides `MLAI_API_KEY`. Without a key, and without `-allow-plain-http`, the
bridge refuses to start on a non-loopback address. `-direct-only` together with `-direct=false`
is refused too.

On a loopback address, `serve` announces itself in `~/mlai-bridge/serve.json` (readable only by
you) so that `claude`, `codex` and `status` can find it; see [Which bridge is used](#which-bridge-is-used).

Recommended for daily use:

```powershell
.\mlai-wrap.exe serve -prewarm
``` See [CONFIGURATION.md](CONFIGURATION.md) and
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
2. The bridge started with `serve` on this machine. `serve` writes its address and a random
   secret to `~/mlai-bridge/serve.json` (mode 0600). Before sending any key or prompt, the
   launcher sends a fresh random challenge to `/healthz` and checks the answer, an HMAC made with
   that secret. Whatever else listens on the port is never used. The key comes from
   `MLAI_API_KEY`; a bridge that rejects it is skipped. The launcher prints
   `using the bridge at <url>`.
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
mlai-wrap status [--url URL] [--json]
```

Shows where requests spend their time. Without `--url` it uses the bridge started with `serve`
(verified as described in [Which bridge is used](#which-bridge-is-used)), then `MLAI_BRIDGE_URL`,
then `http://127.0.0.1:8197`. The key from `MLAI_API_KEY` is sent only to a URL you named or to
the verified `serve` bridge.

```
mlai-bridge 1.0.0 at http://127.0.0.1:8197: ok, signed in: yes, uptime 58s
build:      mlai-wrap 0.1.1 (mlai-bridge 1.0.0, commit 8fa0f29, committed 2026-11-26T14:34:21Z, go1.26.11, windows/amd64)
direct:     on, CLI fallback on; prewarm on; keeper wait 30s
keeper:     token ready (age 44s); last start took 14.5s
fallback:   none since start
requests:   last 5: direct 5; 0 active now
  queue wait   p50 0s      p95 0s      (5)
  keeper wait  p50 0s      p95 0s      (5)
  first text   p50 1.7s    p95 4s      (3)
  total        p50 3.8s    p95 4.7s    (5)
```

| Line | Meaning |
| --- | --- |
| `build` | package version, Git commit (`(modified)` for a build with uncommitted changes), commit time, Go version and platform of the running bridge |
| `direct` | whether direct mode, direct-only and prewarm are on, and the keeper wait |
| `keeper` | whether a run token is ready and how old it is, whether a keeper run is starting, how long the last start took, failures in a row and the last error |
| `fallback` | the last reason a request did not use the direct path, how long ago, and how often since start |
| `requests` | the last 200 model requests by path: `direct`, `cli` (fallback), `cache`, `refused` (direct-only) |
| `queue wait` | time waiting for a free slot |
| `keeper wait` | time a direct request waited for a run token |
| `first text` | time to the first streamed text sent to the client (streams only) |
| `total` | whole request |

`--json` prints `GET /v1/status` ([API.md](API.md#get-v1status)). Exit code `1` when no healthy
bridge answers, `2` for bad arguments. A bridge older than 0.1.1 has no `/v1/status`; `status`
then prints the first line and `details: not available`.

```bash
mlai-wrap status
mlai-wrap status --url http://127.0.0.1:8197 --json
```

## doctor

```
mlai-wrap doctor
```

Checks that `mlai`, `claude` and `codex` are on `PATH` and that `mlai` is signed in. It prints
`yes` or `no` for each. Exit code `1` if `mlai` is missing or signed out, otherwise `0`. A
missing `claude` or `codex` does not change the exit code.

## version and help

`mlai-wrap version` prints the build identity, for example
`mlai-wrap 0.1.1 (mlai-bridge 1.0.0, commit 8fa0f29, committed 2026-11-26T14:34:21Z, go1.26.11, windows/amd64)`.
A local build shows `dev` as the package version; release builds carry the tag's version. `mlai-wrap help` prints the usage text. An unknown
command prints the usage text to stderr and exits with `2`.

## npm launcher

The npm package starts the binary from the platform package `@mlai-wrap/<os>-<cpu>`.

| Env var | Meaning |
| --- | --- |
| `MLAI_WRAP_BINARY` | run this binary instead of the platform package |

```powershell
$env:MLAI_WRAP_BINARY = 'C:\path\to\mlai-wrap.exe'
```
