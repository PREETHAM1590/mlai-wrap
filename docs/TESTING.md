# Testing

Three levels: automated tests that need no network, a manual end-to-end check on Windows, and a
release check for the npm packages.

## Automated

```bash
gofmt -l .          # must print nothing
go vet ./...
go test ./...
go test -race ./...
```

CI runs `gofmt -l .`, `go vet ./...` and `go test -race ./...` on Linux
(`.github/workflows/ci.yml`).

The tests need no network and no real `mlai`. The test binary doubles as a stub `mlai` when
`MLAI_STUB=1` is set.

| File | What it covers |
| --- | --- |
| `bridge_test.go` | request parsing, cache, error mapping |
| `security_test.go` | auth gate, `Host` and content-type checks, body limits, log safety, runner hardening, keeper, direct-mode fallback |
| `agents_test.go` | Anthropic tool use, `/v1/responses`, cut streams, a keeper run that ends without a token, hook-failure answers, CLI error formats |
| `cli_test.go` | launcher argument parsing, client environment and `--settings`, exit codes, private and running bridges, `.cmd` shims, `env`, `models`, `status`, `doctor` |
| `isolate_test.go` | the `MLAI_HOME` link, the child environment, cp1252-safe prompts |

## Manual end-to-end check on Windows

This check needs a signed-in `mlai`, Claude Code and Codex. It runs real ML AI Code runs on your
plan. Run it in PowerShell from the repository root.

1. Build the binary:

   ```powershell
   go build -o mlai-wrap.exe .
   ```

2. Check the setup:

   ```powershell
   .\mlai-wrap.exe doctor
   ```

3. In one terminal, start the bridge and leave it running:

   ```powershell
   .\mlai-wrap.exe serve
   ```

4. In a second terminal, in a new empty folder, run Claude Code (replace `<path>` with the
   repository folder):

   ```powershell
   & <path>\mlai-wrap.exe claude -m pixis -- -p "Create hello.txt containing exactly CLAUDE_OK, then reply: done." --allowedTools "Write Bash"
   Get-Content hello.txt
   ```

   `Get-Content` should print `CLAUDE_OK`.

5. In the same folder, run Codex:

   ```powershell
   Set-Content secret-word.txt CODEX_OK
   & <path>\mlai-wrap.exe codex -m pixis -- exec --skip-git-repo-check "Read the file secret-word.txt in the current folder and reply with exactly the word it contains."
   ```

   The reply should be `CODEX_OK`.

6. Confirm that the serve terminal, or `~/mlai-bridge/wrap.log`, shows `run token refreshed`.

7. Confirm that no new Windows Terminal tabs opened during steps 3 to 5.

8. Check the API directly:

   ```powershell
   Invoke-RestMethod -Uri http://127.0.0.1:8197/v1/messages -Method Post -ContentType 'application/json' -Body '{"model":"pixis","max_tokens":2000,"messages":[{"role":"user","content":"Reply with exactly: OK"}]}'
   ```

   Use a `max_tokens` of 2000 or more: reasoning uses tokens first, and a small value can return
   an empty reply.

If a step fails, see [TROUBLESHOOTING.md](TROUBLESHOOTING.md).

## Release check

CI builds five binaries and publishes them to npm when a `v*` tag is pushed (see
[CONTRIBUTING.md](../CONTRIBUTING.md#releasing)). To check the packages locally:

1. Build the five binaries into `dist/`, named as in `.github/workflows/ci.yml`:
   `mlai-bridge_linux_amd64`, `mlai-bridge_linux_arm64`, `mlai-bridge_darwin_amd64`,
   `mlai-bridge_darwin_arm64` and `mlai-bridge_windows_amd64.exe`. Each is built with
   `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -ldflags '-s -w' -o dist/<name> .`.
2. Build the npm package folders:

   ```bash
   node scripts/npm-build.mjs X.Y.Z dist
   ```

   It prints one folder per package.
3. Run `npm pack` in each printed folder.
4. Install the tarballs into a temporary prefix, for example
   `npm install -g --prefix <temp folder> <tarballs>`, and run `mlai-wrap version` from it.

A tag with a `-` (for example `v0.1.1-beta.1`) is published to the npm `next` dist-tag instead of
`latest`.
