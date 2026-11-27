# Deployment

For a single machine, run `mlai-wrap serve` and nothing else is needed. This page covers other
setups: a remote bind, systemd, Docker and signing in without a browser.

## Headless sign-in

`mlai login` listens on a random localhost port and prints an auth URL. On a remote box
(VPS, no local browser):

The port is only known once `mlai login` is running, so start it first and open the tunnel
second:

1. In one SSH session on the host, start the sign-in without a browser and leave it running:

   ```bash
   MLAI_NO_BROWSER=1 mlai login
   ```

   Note the port in the printed `http://127.0.0.1:<port>/…` URL.
2. On your laptop, in a second terminal, forward that port and keep the tunnel open:

   ```bash
   ssh -N -L <port>:127.0.0.1:<port> user@host
   ```

3. Open the printed URL in your laptop's browser. `mlai login` finishes on the host once you
   have signed in; then close the tunnel.

## Exposing it beyond localhost

Both shapes below need a key. Without one the bridge only binds to loopback, rejects any `Host`
header that is not `localhost` or a loopback IP, and requires `Content-Type: application/json`
on POST. A reverse proxy forwards a public `Host`, so without a key every proxied request would
fail with `403 forbidden_host`, and a bridge that accepted it unauthenticated would be open to
anyone who can reach the proxy. Set `MLAI_API_KEY` on the bridge and have clients send it.

**Behind your own reverse proxy (recommended).** Keep the bridge on loopback with `MLAI_API_KEY`
set and add a TLS location:

```nginx
location ^~ /mlai/ {
    proxy_pass http://127.0.0.1:8197/;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_buffering off;         # SSE must not be buffered
    proxy_cache off;
    proxy_read_timeout 3600s;
    client_max_body_size 8m;
}
```

Then clients use `https://your.host/mlai/v1` with the key.

**Direct bind (trusted networks only).** The bridge has no TLS, so the key and every prompt
travel in clear. It refuses a non-loopback address unless it has a key and you opt in with
`-allow-plain-http` (`MLAI_ALLOW_PLAIN_HTTP=true`). Prefer the reverse proxy above; otherwise use
a firewall allowlist, never the open internet.

Generate a key and keep it out of git. Pass it through the environment, not `-api-key`, which
shows up in process lists:

```bash
(umask 077 && openssl rand -hex 24 > .apikey)
MLAI_API_KEY="$(cat .apikey)" ./mlai-bridge -bind 0.0.0.0 -allow-plain-http
```

Auth accepts `Authorization: Bearer <key>`, `x-api-key: <key>` or `api-key: <key>`. A key in the
query string is not accepted. A wrong or missing key returns `401 invalid_api_key` in the
provider's error shape. CORS preflight is answered for browser clients.

To use such a bridge from the launcher, pass its URL and key:

```bash
MLAI_API_KEY="$(cat .apikey)" mlai-wrap claude --url https://your.host/mlai
```

Plain `http` to a non-loopback host is refused by the launcher.

## Systemd

The repo ships a hardened unit, `mlai-bridge.service`. It runs as a dedicated `mlai` user with
state in `/var/lib/mlai-bridge` and reads the key from an environment file instead of the
command line.

```bash
sudo useradd --system --home-dir /var/lib/mlai-bridge --shell /usr/sbin/nologin mlai
sudo install -d -m 0755 /etc/mlai-bridge
sudo install -m 0600 -o root -g root /dev/null /etc/mlai-bridge/env
echo "MLAI_API_KEY=$(openssl rand -hex 24)" | sudo tee /etc/mlai-bridge/env >/dev/null
sudo npm install --prefix /opt/mlai mlai-code
sudo install -D -m 0755 mlai-bridge /opt/mlai-bridge/mlai-bridge
sudo install -m 0644 mlai-bridge.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now mlai-bridge
```

Key points of the unit:

- `EnvironmentFile=/etc/mlai-bridge/env` (root:root, `0600`) holds `MLAI_API_KEY=...`.
- `User=mlai`, `StateDirectory=mlai-bridge` (`0700`), `HOME=/var/lib/mlai-bridge`, `UMask=0077`.
  Run `mlai login` as that user (with the same `HOME`) once, so `~/.mlai` lives there.
- `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `PrivateDevices`, no capabilities,
  `NoNewPrivileges`. `MemoryDenyWriteExecute` and `RestrictNamespaces` are left off on purpose:
  Node needs JIT and the CLI sandbox needs namespaces.
- Logs go to the journal: `journalctl -u mlai-bridge`.

## Docker

The `Dockerfile` has two stages. The first builds the Go binary with `CGO_ENABLED=0` on
`golang:1.26-alpine`. The second is `node:22-alpine` with the `mlai` CLI installed
(`mlai-code`, version set by the build argument `MLAI_CODE_VERSION`, default `0.3.27`), `tini`
as the init process, and the binary at `/usr/local/bin/mlai-bridge`. The container runs as the
`node` user.

Defaults set in the image: `MLAI_BIND=0.0.0.0` (needed to publish the port),
`MLAI_ALLOW_PLAIN_HTTP=true` (plain HTTP inside the container only: publish the port as
`127.0.0.1:8197:8197`, or put TLS in front),
`MLAI_WORKDIR=/data/workspace`, `MLAI_MODEL=pixis-opus`, `MLAI_PORT=8197`. Because the bind
address is not loopback, the bridge refuses to start without `MLAI_API_KEY`. Pass the key at run
time and never bake it into the image.

`/home/node/.mlai` is a volume. Mount a signed-in `~/.mlai` into it read-write, because the CLI
refreshes tokens there.

Build and run:

```bash
docker build -t mlai-bridge:latest .      # or: make docker
docker run --rm -p 127.0.0.1:8197:8197 \
  --env-file ./docker.env \
  -v "$HOME/.mlai:/home/node/.mlai" \
  mlai-bridge:latest
```

`docker.env` holds one line, `MLAI_API_KEY=<key>`, and must stay out of git. Publishing the port on
`127.0.0.1` keeps it local; put a TLS reverse proxy in front for remote use, as described above.
