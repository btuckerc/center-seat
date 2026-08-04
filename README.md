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
