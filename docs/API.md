# API

The bridge exposes OpenAI-compatible and Anthropic-compatible endpoints. Default address:
`http://127.0.0.1:8197`.

## Endpoints

| Method and path | What it is |
| --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions (stream and non-stream), tool calls |
| `POST /v1/messages` | Anthropic Messages (stream and non-stream), `tool_use` blocks |
| `POST /v1/messages/count_tokens` | token estimate |
| `POST /v1/responses` | OpenAI Responses, used by Codex; stateless |
| `GET /v1/models` | model ids ([MODELS.md](MODELS.md)) |
| `GET /healthz` | status, version, `uptime_s` and `signed_in`; the sign-in state is cached for 60 s. With `?challenge=<16-128 hex digits>`, a bridge started with `serve` adds `proof` (see [HOW-IT-WORKS.md](HOW-IT-WORKS.md#finding-the-shared-bridge)) |
| `GET /v1/status` | build identity, keeper state, last fallback, timings of the last 200 requests ([below](#get-v1status)) |
| `GET /metrics` | metrics |

`/healthz` and `/metrics` stay open when a key is set; `/v1/status` needs the key.

Request header `X-MLAI-Direct-Only: true` (also `1` or `yes`): never fall back to the CLI for this
request. If the direct path fails, the answer is `503 unavailable` with the reason. Codex can send
it with `-c 'model_providers.mlai.http_headers={"X-MLAI-Direct-Only"="true"}'`; for Claude Code
start the bridge with `-direct-only`. Every response carries `X-Request-Id`.
Identical requests are cached and carry `X-Cache: HIT` or `X-Cache: MISS`. Usage fields are
described in [HOW-IT-WORKS.md](HOW-IT-WORKS.md#token-accounting).

## Client setup

OpenAI SDK or any compatible client:

```
base_url = http://127.0.0.1:8197/v1
api_key  = anything when no key is set on the bridge; otherwise the bridge's MLAI_API_KEY
model    = pixis-opus
```

Claude Code and other Anthropic clients:

```
ANTHROPIC_BASE_URL = http://127.0.0.1:8197
ANTHROPIC_API_KEY  = anything when no key is set on the bridge; otherwise the bridge's MLAI_API_KEY
```

To start Claude Code or Codex without setting these yourself, use `mlai-wrap claude` and
`mlai-wrap codex` ([COMMANDS.md](COMMANDS.md#claude-and-codex)). Both read the key from
`MLAI_API_KEY` when they use a bridge that is already running.

## Examples

```bash
curl http://127.0.0.1:8197/healthz

curl http://127.0.0.1:8197/v1/chat/completions -H 'Content-Type: application/json' -d '{
  "model":"pixis-opus",
  "messages":[{"role":"user","content":"say hi"}]
}'

curl -sS -N http://127.0.0.1:8197/v1/messages -H 'Content-Type: application/json' \
  -d '{"model":"pixis-opus","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"say ok"}]}'
```

With a key:

```bash
curl http://127.0.0.1:8197/v1/models -H "Authorization: Bearer $MLAI_API_KEY"
```

Authentication accepts `Authorization: Bearer <key>`, `x-api-key: <key>` or `api-key: <key>`. See
[CONFIGURATION.md](CONFIGURATION.md#api-key-and-exposure).

Without a key, every POST needs `Content-Type: application/json`.

## Error codes

Errors are returned in the provider's native shape with a stable code:

| HTTP | code | when |
| --- | --- | --- |
| 400 | `invalid_request` | malformed JSON, missing `messages` |
| 400 | `upgrade_required` | CLI too old (`mlai update`) |
| 401 | `not_signed_in` | `mlai login` missing or expired |
| 401 | `invalid_api_key` | wrong or missing key when the bridge has one |
| 403 | `forbidden_host` | no key set and the `Host` header is not `localhost` or a loopback IP |
| 404 | `model_not_found` | unknown model id |
| 415 | `unsupported_media_type` | POST without `Content-Type: application/json` (enforced when no key is set) |
| 429 | `concurrent` | no free slot within `-queue-wait` |
| 429 | `rate_limited` | upstream session or hourly ceiling |
| 429 | `daily_cap` | allowance or credits exhausted |
| 502 | `upstream_error` | CLI failed for another reason |
| 503 | `unavailable` | CLI missing or not executable; or direct-only and the direct path failed (the message gives the reason) |
| 504 | `timeout` | request exceeded `-timeout` |

`429` responses carry `Retry-After`.

## GET /v1/status

```json
{
  "build": {"package": "0.1.1", "bridge": "1.0.0", "commit": "8fa0f29…", "built": "2026-11-26T14:34:21Z",
            "go": "go1.26.11", "platform": "windows/amd64"},
  "uptime_s": 58, "direct": true, "direct_only": false, "prewarm": true, "keeper_wait_s": 30,
  "keeper": {"token_ready": true, "token_age_s": 44, "starting": false, "last_start_s": 14.5,
             "last_start_ago_s": 58, "failures_in_a_row": 0},
  "last_fallback": {"at": "2026-11-26T14:37:11Z", "ago_s": 120, "reason": "gateway returned HTTP 500"},
  "fallbacks_total": 1,
  "recent": {"count": 5, "by_path": {"direct": 5},
             "queue_wait": {"count": 5, "p50_s": 0, "p95_s": 0},
             "keeper_wait": {"count": 5, "p50_s": 0, "p95_s": 0},
             "first_text": {"count": 3, "p50_s": 1.7, "p95_s": 4.0},
             "total": {"count": 5, "p50_s": 3.8, "p95_s": 4.7}},
  "active_requests": 0, "requests_total": 5, "requests_failed_total": 0
}
```

`last_fallback` is left out when there was none. `mlai-wrap status` prints the same data. CORS preflight is answered for browser clients.

Error messages sent to clients are fixed text. Raw CLI output appears only in the bridge logs.
