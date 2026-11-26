# Configuration

Every setting is a flag, an environment variable, or both. A flag overrides its environment
variable. The `claude` and `codex` launcher options are in [COMMANDS.md](COMMANDS.md).

| Flag | Env var | Default | Meaning |
| --- | --- | --- | --- |
| `-port` | `MLAI_PORT` | `8197` | listen port |
| `-bind` | `MLAI_BIND` | `127.0.0.1` | listen address; a non-loopback address requires `MLAI_API_KEY` (or `-api-key`), otherwise the bridge refuses to start |
| `-api-key` | `MLAI_API_KEY` | empty | require `Authorization: Bearer <key>` (or `x-api-key`, `api-key`) on `/v1/*`; `/healthz` and `/metrics` stay open. Prefer the env var: flags show in process lists |
| `-mlai` | `MLAI_BIN` | `mlai` | path to the mlai CLI |
| `-workdir` | `MLAI_WORKDIR` | `~/mlai-bridge/workspace` | folder the agent runs in |
| `-model` | `MLAI_MODEL` | `pixis-opus` | default model id |
| `-concurrency` | `MLAI_CONCURRENCY` | `1` | max parallel CLI runs |
| `-direct-concurrency` | `MLAI_DIRECT_CONCURRENCY` | `8` | max parallel gateway requests |
| `-queue-wait` | none | `30s` | how long a request waits for a free slot |
| `-timeout` | none | `10m` | wall clock limit per request, CLI and direct |
| `-cache-ttl` | none | `10m` | identical-request cache TTL (`0` disables) |
| `-cache-size` | none | `512` | cache entries |
| `-effort` | `MLAI_EFFORT` | empty | reasoning effort: `low`, `medium`, `high` or `max` |
| `-direct` | `MLAI_DIRECT` | `true` | gateway direct mode; `false` uses the CLI only |
| `-isolate-skills` | `MLAI_ISOLATE_SKILLS` | `true` | run `mlai` without your Claude Code skills and agents |
| none | `MLAI_BRIDGE_URL` | `http://127.0.0.1:8197` | bridge URL for `claude`, `codex`, `env`, `models` and `status` |
| none | `MLAI_BRIDGE_KEY` | set by the launcher | carries the bridge key to Codex; you do not set it yourself |
| none | `MLAI_HOME` | set by the bridge | set for `mlai` child processes when skill isolation is on |
| none | `MLAI_WRAP_BINARY` | empty | npm launcher: run this binary instead of the platform package |

## Skill isolation

`mlai` copies `~/.claude/skills` and `~/.claude/agents` into every request, and its Windows
engine reads that request as cp1252. A character such as `”` in any skill crashed it:

```
UnicodeDecodeError: 'charmap' codec can't decode byte 0x9d
```

The bridge therefore runs `mlai` with `MLAI_HOME=~/mlai-bridge/home/.mlai`. That path is a link
(a junction on Windows) to your real `~/.mlai`: sign-in and sessions are shared, skills and
agents are not.

On Windows the bridge also replaces the few prompt characters that would still crash the engine.
For example `“` and `”` become `"`.

Turn isolation off with `MLAI_ISOLATE_SKILLS=false` or `-isolate-skills=false`.

**Never delete `~/mlai-bridge/home` recursively.** The link points at your real `~/.mlai`, and a
recursive delete can remove your sign-in and sessions. Remove the link itself with `rmdir`.

## API key and exposure

Without a key the bridge:

- starts only on a loopback address,
- accepts only requests whose `Host` header is `localhost` or a loopback IP (otherwise
  `403 forbidden_host`),
- requires `Content-Type: application/json` on every POST (otherwise
  `415 unsupported_media_type`).

Set `MLAI_API_KEY` to require a key, which is also required to bind a non-loopback address. The
bridge accepts `Authorization: Bearer <key>`, `x-api-key: <key>` or `api-key: <key>`. A key in
the query string is not accepted. A wrong or missing key returns `401 invalid_api_key`.

Pass the key through the environment, not `-api-key`, and keep it out of git. Reverse proxy,
direct bind, systemd and Docker setups are in [DEPLOYMENT.md](DEPLOYMENT.md).
