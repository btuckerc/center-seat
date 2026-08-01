# Architecture

CenterSeat is one service with strict module boundaries, not a fleet of
microservices. That keeps the query path easy to reason about while the
provider adapters and stores scale independently.

```text
Client
  ├─ GET /v1/movie-suggestions ── canonical provider title matching
  ├─ POST /v1/showtime-queries ── real discovery without seat claims
  │ POST /v1/seat-queries ─────── exact-seat query when inventory is connected
  │ GET /v1/seat-queries/{id}/recommendations/{rank}
  │                              lazy live map for one selected alternative
  ▼
HTTP contract ── idempotency / validation / request ID / ETag
  ▼
Query service
  ├── cached discovery adapter ── configured showtime source
  ├── constraint planner ──────── local filtering and candidate scoring
  ├── bounded fan-out ─────────── live inventory adapters (5–12 candidates)
  ├── geometry ranker ─────────── contiguous blocks + confidence penalties
  └── final verifier ──────────── one uncached read-only availability check
  ▼
Winner + alternatives + score explanation + freshness + booking URL
```

## Latency budget

| Stage | Target | Failure behavior |
| --- | ---: | --- |
| Request normalization | 5 ms | Return a 422 problem detail |
| Cached discovery | 50 ms p95 | Fall back to a secondary licensed feed |
| Candidate pruning | 10 ms p95 | Deterministic, local operation |
| Live seat read | 5 s per candidate | Return partial coverage with normalized failure reasons |
| Ranking | 20 ms p95 | Exclude unrankable low-confidence maps |
| Winner verification | 5 s hard timeout | Mark result partial; never create a hold |
| End to end | 3 s typical | Preserve explainable partial results during upstream degradation |

## Cache policy

| Record | Freshness |
| --- | --- |
| Venue identity | 7–30 days |
| Movie metadata | 12–24 hours |
| Showtimes beyond 48 hours | 15–60 minutes |
| Showtimes within 48 hours | 5–10 minutes |
| Auditorium layouts | 1–30 days, keyed by payload hash |
| Live availability | 0–8 seconds |

Redis is the intended hot cache and concurrency-control surface. PostgreSQL
preserves the normalized schema for provider observations and reproducible query
results. The current read path keeps short-lived request state in-process; the
container definitions provision the durable services for the next persistence
step. Production startup never selects fixture data implicitly.

## Reliability rules

- Per-provider deadlines and concurrency limits protect the query path.
- Inventory failures degrade individual candidates, not the whole search.
- A successfully loaded map with no eligible block or an excessive live price
  is counted as a normal exclusion, not mislabeled as an upstream failure.
- Partial responses include normalized failure categories without leaking
  provider payloads or request details.
- Idempotency keys bind to canonical request hashes; key reuse with another body
  returns a conflict.
- `GET` results use strong ETags and short private cache headers.
- Provider responses are normalized before transport handlers see them.
- Geometry confidence is part of ranking and the public explanation.
- A single-ticket dead-center query exposes a four-position ideal zone and all
  currently available equivalent choices; unavailable positions remain visible
  rather than shifting the geometric target.
- Initial responses keep alternative maps compact. Opening an alternative
  refreshes only that screening, limiting upstream fan-out while making results
  explorable.
- The final check is read-only. Query traffic never manipulates market
  availability by holding seats.
- If the leading screening loses its eligible block during final verification,
  it is removed and the next ranked candidate is verified before promotion.
- The personal Fandango adapter is rejected outside local/development mode and
  has no code path for token, reservation, cart, wallet, payment, or purchase
  operations.
- JSON logs, health checks, readiness checks, and Prometheus metrics are built
  into the service surface.

## Scale path

The stateless API can be replicated behind a load balancer. Shared idempotency,
query results, and rate-limit leases move to Redis/PostgreSQL when production
providers are enabled. Candidate fan-out is bounded per provider and per tenant,
so horizontal scale does not become an uncontrolled upstream request
multiplier. The normalized schema allows scheduled discovery ingestion to run
separately without changing the query contract.
