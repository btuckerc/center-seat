# Mac mini deployment

The active checkout is `/Users/admin/src/centerseat`. Production uses the base
Compose file plus `deploy/macmini/compose.override.yaml`.

```sh
cd /Users/admin/src/centerseat
git pull --ff-only
docker compose -p centerseat \
  -f compose.yaml \
  -f deploy/macmini/compose.override.yaml \
  up -d --build
```

The override:

- binds the web app to `127.0.0.1:13000`;
- keeps the API off host ports;
- restarts services after Docker or host restarts;
- adds a web health check.

Cloudflare Tunnel routes `movies.angl.gg` to the loopback web port.

## Agent token

`deploy/macmini/agent-token.sh status|rotate|print` manages
`CENTERSEAT_AGENT_TOKEN` in `.env` without showing it; see
[`docs/agents.md`](../../docs/agents.md#managing-the-credential).

## Verify

```sh
docker compose -p centerseat \
  -f compose.yaml \
  -f deploy/macmini/compose.override.yaml \
  ps
curl --fail http://127.0.0.1:13000/api/providers
curl --fail https://movies.angl.gg/api/providers
```
