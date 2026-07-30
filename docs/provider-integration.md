# Provider integration

CenterSeat models discovery and seat inventory as separate capabilities even
when one platform supplies both. The default discovery adapter now uses Open
Cinema; Atom remains an optional full-inventory adapter.

## Implemented Open Cinema path

Open Cinema issues self-service API keys and provides live indie, repertory, and
arthouse screenings with provider checkout links. CenterSeat authenticates
server-side, performs one title-and-location query with cursor pagination,
normalizes formats and accessibility fields, filters the complete requested date
range in theater-local time, and rejects unsafe or placeholder checkout URLs.

Open Cinema does not expose auditorium layouts or live seat states. Its
showtimes therefore have no inventory provider, never enter the exact-seat
ranking path, and are returned through the discovery-only endpoint with an
explicit warning. `OPEN_CINEMA_API_KEY` is required and
`OPEN_CINEMA_API_BASE_URL` defaults to `https://opencinema.app`.

## Optional Atom path

The adapter authenticates with Atom's server-side `x-api-key`, looks up nearby
supported venues, batches multi-venue showtime discovery in Atom's seven-day
windows, and calls the read-only auditorium discovery endpoint for current seat
status. It repeats the winning map read before returning the recommendation and
never calls the lease endpoint. Unknown seat states fail closed as blocked.

`ATOM_API_KEY` is required. `ATOM_API_BASE_URL` defaults to Atom production, and
`ATOM_PARTNER_ID` is optional unless assigned during onboarding. Atom's venue
API requires latitude/longitude and limits radius to 80 km. Checkout links are
used only when Atom supplies a valid HTTPS `atomtickets.com` URL.

## Personal local Fandango path

`CENTERSEAT_PROVIDER_MODE=fandango-local` is an opt-in full-inventory adapter
that is accepted only with `CENTERSEAT_ENV=local` or `development`. It
resolves a title through Fandango's anonymous autocomplete response, queries
showtime groupings for every requested date, filters candidates locally, then
reads only the bounded set of required seat maps. Exact provider coordinates,
neighbor IDs, seat types, ticket price/fee hints, and observed availability
states feed the existing ranker.

The adapter has a hardcoded GET path allowlist, rejects redirects, sends no
cookie or account state, limits request concurrency, spaces request starts, and
refreshes the winning map without creating a hold. It contains no checkout,
token, reservation, cart, wallet, payment, or purchase implementation. Current
Fandango terms and robots restrictions make this a personal local integration,
not a hosted production dependency. See `docs/fandango-research.md`.

## Discovery adapter

An adapter must return normalized movies, venues, showtimes, presentation
formats, accessibility attributes, price hints, provider IDs, and booking links.
The ingestion layer should preserve the raw payload hash and observation window
so changes are auditable.

Required production behavior:

- Conditional requests (`ETag`, `If-Modified-Since`, or provider cursor)
- Provider-specific retry classification with jitter
- Explicit rate-limit accounting
- Stable external-ID mapping for movies and venues
- Timezone-aware local showtimes
- Licensed commercial usage and a support contact

## Inventory adapter

An adapter must report a layout and a live availability observation. Static
layout data and volatile availability should be fetched and cached separately.

Required fields:

- Row and seat labels
- Real coordinates when available
- Seat type (standard, recliner, wheelchair, companion, sofa)
- State (available, sold, held, broken, house)
- Auditorium and screen boundary when supplied
- Observation time and permitted cache lifetime
- Direct booking link when the provider supplies or contractually defines one

Adapters declare a confidence grade: `exact_coordinates`,
`rendered_geometry`, `row_geometry`, `label_heuristic`, or `visual_inference`.
The query can enforce a minimum grade.

## Launch sequence

1. Contract and commercial review with one discovery vendor.
2. Shadow-ingest one market and compare coverage with theater sites.
3. Add one authorized inventory platform or exhibitor.
4. Run synthetic searches against fixed showtimes and compare map snapshots.
5. Enable a small geography with provider budgets and dashboards.
6. Expand by ticketing platform, not one theater UI at a time.

Do not use an order or seat hold as an availability probe. Do not make
unauthorized scraping a required production dependency. Purchase support should
be a separate, explicit workflow with new threat modeling, confirmation,
payment, and cleanup semantics.

The opt-in local Fandango adapter notes and local HAR-analysis procedure live in
`docs/fandango-research.md`. Captures are never replayed by the analyzer.
