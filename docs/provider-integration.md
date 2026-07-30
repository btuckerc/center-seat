# Provider integration

CenterSeat treats discovery and seat inventory as separate capabilities because
no universal public API reliably provides both.

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
- Direct booking link

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
