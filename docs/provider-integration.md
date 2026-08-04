# Provider integration

Discovery and seat inventory are separate capabilities. A provider may support
one or both.

## Current adapters

### Open Cinema

Open Cinema supplies self-service showtimes and checkout links for independent
cinemas. It does not supply seat maps. Set `OPEN_CINEMA_API_KEY` and use
`CENTERSEAT_PROVIDER_MODE=opencinema`.

### Atom

Atom supplies showtimes and seat inventory with approved partner access. Set
`ATOM_API_KEY` and use `CENTERSEAT_PROVIDER_MODE=atom`. The adapter reads
availability twice for the winner and never calls the seat-lease endpoint.

### Fandango local

The local adapter resolves titles, fetches showtimes for each requested date,
and reads a bounded set of anonymous seat maps. It is restricted to `local` or
`development`, sends no account state, and contains no checkout code.

See [fandango-research.md](fandango-research.md).

## Adapter contract

A discovery adapter returns normalized movies, venues, showtimes, formats,
accessibility details, price hints, provider IDs, and booking links.

An inventory adapter returns:

- row and seat labels;
- coordinates;
- seat type and availability;
- auditorium geometry when available;
- observation time;
- a provider booking link when permitted.

Unknown or incomplete inventory must fail closed. Keep volatile availability
separate from the more stable auditorium layout.

## Adding a provider

1. Implement the adapter under `backend/internal/providers/`.
2. Normalize provider values into `backend/internal/domain` types.
3. Add fixtures for every observed seat state and layout variant.
4. Add configuration validation and startup checks.
5. Test retries, time zones, pagination, price handling, and final verification.
6. Update `.env.example` and `openapi/v1.yaml` if the public contract changes.

Do not use a hold, cart, or order as an availability probe.
