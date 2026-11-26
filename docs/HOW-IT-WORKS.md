# How it works

```
OpenAI client ─┐
               ├─► mlai-bridge :8197 ──► `mlai -p … --output-format stream-json` ──► code.ml.ai
Claude Code  ──┘
```

The bridge turns the `mlai` agent CLI into OpenAI Chat Completions, Anthropic Messages and
OpenAI Responses endpoints (see [API.md](API.md)). It has two ways to reach the model: direct
mode, which is the default, and the CLI path, which is the fallback.

## Direct mode and the run keeper

Direct mode talks to `code.ml.ai/api/model/v1/chat/completions` with a live run token. Streams
carry real token deltas (TTFT under 1 s, about 110 to 160 tok/s on a warm keeper) and there is no
agent-harness prompt. The bridge falls back to the CLI automatically if the gateway rejects the
run.

The gateway accepts a per-run bearer token (`mlrun_…`) that only the CLI can mint, and it dies
when the run ends. The bridge therefore keeps one CLI run alive and reuses that run's token from
`~/.mlai/sessions/<id>/engine/mcp.json`. When the gateway answers `409 run_finished`, the bridge
opens a fresh keeper run and retries the request once.

```
client ─► bridge ─► code.ml.ai/api/model/v1/chat/completions   (direct: real SSE)
                 └► mlai -y -p (keeper: keeps the run token alive)
```

The keeper:

- is a foreground `mlai -y -p` child of the bridge with a long `sleep` task, in its own folder
  `<workdir>/.keeper/<pid>-<random>`, so two bridges on one workspace never use or stop each
  other's runs. The folder is removed when the bridge stops. It is not `mlai --bg`, which makes Windows Terminal open and close about
  30 tabs every time it runs.
- starts one run at a time, at least 10 s apart (longer after repeated failures). A keeper whose
  token the gateway rejected is stopped before a new one starts.
- with mlai 0.3.27 ends its run after about 40 s: the agent moves the `sleep` to the background
  and ends the run. The token only lasts that long, so the bridge opens a new run about once a
  minute while you work. A request that arrives while a new run is starting waits up to
  `-keeper-wait` (default 30 s) for it, then falls back to the CLI; the run goes on for the next
  request. Each keeper run is one ML AI Code run on your plan.
- retries a run that ends without a token after the cooldown, doubling it after each failure in
  a row (up to 32x). A sign-in, quota or network problem looks the same as an unsupported mlai,
  so nothing is switched off for good. A marker file `unsupported` left by 0.1.0 is removed.
- is owned from start to shutdown: a keeper run that is still starting is killed when the bridge
  stops.
- with `-prewarm`, starts when the bridge starts, so the first request does not wait.

Direct requests are limited by `-direct-concurrency` (default 8). One `-timeout` covers the whole
request: the direct attempt and the CLI fallback share it.

With `-direct-only`, or the header `X-MLAI-Direct-Only: true`, a request whose direct attempt
fails gets `503 unavailable` with the reason (for example `no run token within 30s` or
`gateway returned HTTP 500`) instead of a CLI run.

## Diagnostics

Every model request records its path (`direct`, `cli`, `cache` or `refused`), the time it waited
for a slot, the time it waited for a run token, and, for streams, the time to the first text sent
to the client. The request log line ends with these values:

```
req_17957… "POST" "/v1/messages" 1.853s path=direct queue=0s keeper=0s first_text=1.692s
```

The last 200 requests, the keeper state and the last fallback reason are available from
`GET /v1/status` and `mlai-wrap status`. No token or prompt is included.

## Finding the shared bridge

`serve` on a loopback address writes `~/mlai-bridge/serve.json` (mode 0600) with its URL and a
random 32-byte secret, and removes it when it stops. `/healthz?challenge=<hex>` then includes
`proof`, an HMAC-SHA256 of the challenge keyed with the secret. The launcher reads the file,
sends a fresh challenge and checks the proof before it sends a key or a prompt. A process that
took over the port, or a stale file from a killed bridge, cannot answer, so the launcher starts
a private bridge instead.

Disable direct mode with `-direct=false` or `MLAI_DIRECT=false` to always use the CLI path. That
path is slower, about 10 to 16 s to the first token, because the CLI buffers the whole answer. It
also skips the keeper entirely.

## The CLI path

The CLI runs the full agent harness, so expect about 14k prompt tokens of system context per
call. The Infinite plan covers Pixis and Forge without a meter; other models spend credits.

- The bridge runs `mlai` with `--mode ask`, so the agent answers instead of editing files. The
  prompt is sent to the CLI on stdin.
- The CLI emits the final answer as one event, so the bridge splits that text into chunks for
  streaming. Time to first token equals the model's time to answer. Direct mode streams real
  deltas.
- The CLI accepts at most 30,000 characters per task. Agent requests from Claude Code and Codex
  are larger, so those clients need direct mode.
- `max_tokens` (and `max_output_tokens` on `/v1/responses`) is not enforced: `mlai` has no
  option to cap the answer, so a fallback answer can be longer and use more of your plan than the
  cap allows. Requests with a cap are not refused, because Claude Code sends one with every
  request. Direct mode passes the cap to the gateway; use direct-only where it matters.

## Tool calls

Direct mode passes tools and tool results to the gateway and returns its tool calls as they are.
The rest of this section is about the CLI path.

The CLI has no native function-calling channel, so the bridge emulates it. Tool definitions are
injected into the prompt and the model is asked to answer with
`{"tool_calls":[{"name":"…","arguments":{…}}]}`. The bridge parses that into OpenAI `tool_calls`
(`finish_reason: "tool_calls"`) or Anthropic `tool_use` blocks (`stop_reason: "tool_use"`). Only
tools the request declared are accepted; a call to any other name is not turned into a tool call.
It works in practice, but it is not a wire-level guarantee, so validate arguments on your side.

Server-side tools such as web search are not available.

## Token accounting

Every response reports tokens.

Non-stream (OpenAI):

```json
"usage": {"prompt_tokens": 14619, "completion_tokens": 85,
          "total_tokens": 14704, "prompt_tokens_details": {"cached_tokens": 6144}}
```

Stream: a final chunk with `"choices": []` and the same `usage` object, then `[DONE]`.

Anthropic: `usage.input_tokens`, `usage.output_tokens`, `usage.cache_read_input_tokens`.

## Engine hook warnings

mlai 0.3.x sometimes reports "A Stop hook failed to execute: spawn EPERM" as its answer. This is
mlai's own engine hook. The bridge then returns the model's own reply instead.

## Skill isolation

`mlai` copies `~/.claude/skills` and `~/.claude/agents` into every request, and its Windows
engine reads that request as cp1252. A character such as `”` in any skill crashed it with
`UnicodeDecodeError: 'charmap' codec can't decode byte 0x9d`. To avoid this, the bridge runs
`mlai` with `MLAI_HOME=~/mlai-bridge/home/.mlai`, a link to your real `~/.mlai`: sign-in and
sessions are shared, skills and agents are not. On Windows it also replaces the few prompt
characters that would still crash the engine. Setup, the off switch and a warning about deleting
the link are in [CONFIGURATION.md](CONFIGURATION.md#skill-isolation).

## Caching and concurrency

- Identical requests are served from a cache (`-cache-ttl`, default 10m; `-cache-size`, default
  512). Responses carry `X-Cache: HIT` or `X-Cache: MISS`.
- CLI runs are limited by `-concurrency` (default 1) and direct requests by
  `-direct-concurrency` (default 8). A request waits up to `-queue-wait` (default 30s) for a
  slot, then fails with `429 concurrent`.
- At most 32 `POST /v1/*` requests are admitted at once, before their bodies are read; the rest
  wait up to `-queue-wait`. The cache holds at most 64 MiB, and no answer over 1 MiB is cached.
- Each streamed write must complete within 60 s. A client that stops reading loses the
  connection, which cancels the upstream request and frees its slot.
- The bridge is a single binary that uses only the Go standard library.

## Network exposure

The bridge listens on loopback by default. A non-loopback bind requires `MLAI_API_KEY` and
`-allow-plain-http`. For
remote use see [DEPLOYMENT.md](DEPLOYMENT.md).
