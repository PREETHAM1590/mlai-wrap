# mlai-wrap

mlai-wrap is an OpenAI- and Anthropic-compatible HTTP API in front of the ML AI Code CLI
(`mlai`), plus a launcher that starts Claude Code or Codex against it. You keep using the ML AI
Code subscription you already have: the bridge turns the agent CLI into `/v1/chat/completions`,
`/v1/messages` and `/v1/responses`, so any SDK, editor or gateway can talk to it. The program is
still called `mlai-bridge` when it runs as a server; `mlai-wrap` is the same binary.

Based on [mlai-bridge](https://github.com/codewith-aditya/mlai-bridge) by Aditya Wakharkar
(MIT), with security hardening and the `mlai-wrap` launcher added.

> **Status:** Tested on Windows 11 with mlai 0.3.27, Claude Code 2.1.33x and Codex 0.172.0:
> Claude Code and Codex complete tool tasks through the bridge in direct mode.

## Install

With npm. It installs a prebuilt binary and the commands `mlai-wrap` and `mlai-bridge`; nothing is
downloaded or compiled during the install.

```bash
npm install -g mlai-wrap
```

npm 0.1.0 has the old keeper that opens terminal tabs on Windows; use 0.1.1 or later, or build
from source with `go build -o mlai-wrap.exe .`.

Supported platforms: Windows x64, Linux x64 and arm64, macOS x64 and arm64. The binary comes from
an optional dependency (`@mlai-wrap/<os>-<cpu>`), so do not install with `--omit=optional` or
`--no-optional`.

To build from source on Linux or macOS:

```bash
git clone https://github.com/PREETHAM1590/mlai-wrap && cd mlai-wrap
go build -o mlai-bridge .
```

## Requirements

- Node.js 18+ for the npm launcher
- Go 1.26+ to build from source
- The `mlai` CLI, signed in once with `mlai login`:

```bash
npm install -g mlai-code
mlai login
```

On a machine without a browser, see [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#headless-sign-in).

## Quick start

```bash
mlai-wrap serve                 # in its own terminal; listens on 127.0.0.1:8197
mlai-wrap claude -m pixis       # finds the running bridge by itself
mlai-wrap codex -m pixis
```

Without a running bridge, `claude` and `codex` start a private one for the life of the client.

## Commands

| Command | Purpose |
| --- | --- |
| `serve` | Run the bridge. This is also the default when no command is given. |
| `claude` | Start Claude Code against a bridge. |
| `codex` | Start Codex against a bridge. |
| `env` | Print the variables that point Claude Code or Codex at a running bridge. |
| `models` | List the model ids of a running bridge. |
| `status` | Show the health of a running bridge; exit code 1 when it is down. |
| `doctor` | Check `mlai`, `claude` and `codex` and the `mlai` sign-in. |
| `version` | Print the version. |
| `help` | Print the usage text. |

Options, environment variables and exit codes: [docs/COMMANDS.md](docs/COMMANDS.md).

## How it works

```
Claude Code ──┐                       direct mode (run token from the keeper)
Codex ────────┼─► mlai-bridge :8197 ─────────────────────────────────────────► code.ml.ai gateway
any SDK ──────┘          │
                         └─ CLI fallback: mlai -p … --output-format stream-json ──► code.ml.ai
```

- **Direct mode with a keeper run.** The bridge talks to the code.ml.ai gateway with a run token
  taken from one `mlai` run that it keeps alive, so streams carry real token deltas.
- **CLI fallback.** If the gateway rejects a request or direct mode is off, the bridge runs
  `mlai -p` instead.
- **Skill isolation.** `mlai` runs without your Claude Code skills and agents, because its
  Windows engine cannot read some characters in them.
- **Tool translation.** Tool definitions and tool results are passed through in direct mode and
  emulated in the prompt on the CLI path.

Details: [docs/HOW-IT-WORKS.md](docs/HOW-IT-WORKS.md).

## Configuration

| Flag / env | Default | Meaning |
| --- | --- | --- |
| `-port` `MLAI_PORT` | `8197` | listen port |
| `-model` `MLAI_MODEL` | `pixis-opus` | default model id |
| `-bind` `MLAI_BIND` | `127.0.0.1` | listen address; a non-loopback address needs a key |
| `-api-key` `MLAI_API_KEY` | empty | bearer key for `/v1/*`; prefer the env var |
| `-direct` `MLAI_DIRECT` | `true` | gateway direct mode; `false` uses the CLI only |

All flags and variables: [docs/CONFIGURATION.md](docs/CONFIGURATION.md).

## Testing

```bash
gofmt -l .
go vet ./...
go test ./...
```

`gofmt -l .` must print nothing. Details and a manual end-to-end check:
[docs/TESTING.md](docs/TESTING.md).

## Documentation

- [docs/COMMANDS.md](docs/COMMANDS.md): every command, option, environment variable and exit code
- [docs/CONFIGURATION.md](docs/CONFIGURATION.md): all flags and environment variables, skill isolation, API key
- [docs/HOW-IT-WORKS.md](docs/HOW-IT-WORKS.md): direct mode, the run keeper, tool calls, token accounting
- [docs/API.md](docs/API.md): endpoints, client setup, error codes, curl examples
- [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md): exposing the bridge, systemd, Docker, headless sign-in
- [docs/TESTING.md](docs/TESTING.md): automated tests, manual end-to-end check, release check
- [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md): known problems with cause and fix
- [docs/MODELS.md](docs/MODELS.md): model ids, display names, plans, aliases
- [docs/BENCHMARKS.md](docs/BENCHMARKS.md): methodology and measured numbers
- [CONTRIBUTING.md](CONTRIBUTING.md): rules, development loop, how to add a model

## Known limits

- With mlai 0.3.27 a keeper run and its token last about 40 s, so a new keeper run starts about
  once a minute. Each one is one ML AI Code run on your plan.
- Claude Code and Codex warn about unknown model ids. Both continue.
- The CLI fallback accepts at most 30,000 characters, so the agents need direct mode.
- Server-side tools such as web search are unavailable.
- A small `max_tokens` can return an empty reply, because reasoning uses the tokens first.

## License

MIT
