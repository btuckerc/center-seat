# Mac mini deployment

The Mac mini runs the regular project Compose file with
`deploy/macmini/compose.override.yaml` layered on top.

- The web service is available only on Mac-mini loopback port `13000`.
- The API, PostgreSQL, and Redis services are private to the Compose network.
- Every service restarts automatically after Docker or the host restarts.
- `movies.angl.gg` is routed through the Mac mini's existing Cloudflare Tunnel.
- The public hostname is intentionally not protected by a Cloudflare Access email policy.

Deploy from this directory with:

```sh
docker compose -f compose.yaml -f deploy/macmini/compose.override.yaml up -d --build
```

Verify the local origin and public route with:

```sh
curl --fail --silent --show-error http://127.0.0.1:13000/api/providers
curl --fail --silent --show-error https://movies.angl.gg/api/providers
```
