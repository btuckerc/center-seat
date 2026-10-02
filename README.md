# CenterSeat

Find the best available movie-theater seat across nearby showtimes.

[Open CenterSeat](https://movies.angl.gg)

CenterSeat searches a movie, place, date range, and time window; checks live
seat maps; and ranks contiguous seats by the selected preference. It is
read-only and never holds or buys tickets.

## Run locally

Requirements: Node.js 22, Go 1.25, and Docker.

```sh
cp .env.example .env
# Add a provider key or select the local provider in .env.
docker compose up --build
```

Open `http://localhost:3000`. The API runs at `http://localhost:8080`.

## Providers

| Mode | Use | Seat maps |
| --- | --- | --- |
| `opencinema` | Self-service showtime discovery | No |
| `atom` | Partner showtimes and inventory | Yes |
| `fandango-local` | Personal, local read-only queries | Yes |

`fandango-local` only runs when `CENTERSEAT_ENV` is `local` or `development`.
See [provider integration](docs/provider-integration.md) and
[Fandango notes](docs/fandango-research.md) for details.

## Freshness

A seat query is a snapshot: `expires_at` (about 8 seconds) bounds the cached
result, and `refresh_until` (15 minutes) bounds how long each rank can be
re-verified. The UI re-checks a rank before showing an aged map and before
every provider handoff; after `refresh_until` it asks for a new search. The
API answers expired snapshots with `410` and a problem `code`.

## Share links

The URL carries the whole search, including an explicit location (`near`, or
`lat`/`lon`) and IANA timezone (`tz`), so a recipient's browser never
substitutes its own. Dates and times are interpreted in `tz`. Add `run=1` to
start the search on open. **Copy link** in the results produces one.

## Agent interface

Set `CENTERSEAT_AGENT_TOKEN` to enable bearer-authenticated agent routes on the
web origin (they return 404 when unset). `CENTERSEAT_PUBLIC_URL` sets the origin
used in returned share links.

- JSON: `POST /api/agent/search`, `GET /api/agent/recommendation`,
  `GET /api/agent/movies`, `GET /api/agent/providers`
- MCP (Streamable HTTP, stateless): `POST /api/mcp`
- Agent instructions: [`/llms.txt`](public/llms.txt)

Results include a checkout handoff: exact seats, ticket count, venue,
timezone-labelled start, and provider URL. Agents must select exactly those
seats and stop before payment.

## Development

```sh
npm ci
npm run dev
```

Run all checks with:

```sh
make test
npm run lint
```

## Layout

```text
app/                 web app
backend/             Go API, ranking, and providers
openapi/v1.yaml      API contract
deploy/macmini/      production Compose override
docs/                implementation notes
```

The production layout is described in [docs/architecture.md](docs/architecture.md).
