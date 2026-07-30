# Architecture

CenterSeat is one service with strict module boundaries, not a fleet of
microservices. That keeps the query path easy to reason about while the
provider adapters and stores scale independently.

```text
Client
  │ POST /v1/seat-queries
  ▼
HTTP contract ── idempotency / validation / request ID / ETag
  ▼
Query service
  ├── cached discovery adapter ── licensed showtime feed
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
| Live seat fan-out | 1.8 s hard timeout | Return partial coverage explicitly |
| Ranking | 20 ms p95 | Exclude unrankable low-confidence maps |
| Winner verification | 1.8 s hard timeout | Mark result partial; never create a hold |
| End to end | 2.5 s p95 | Preserve explainable partial results |

## Cache policy

| Record | Freshness |
| --- | --- |
| Venue identity | 7–30 days |
| Movie metadata | 12–24 hours |
| Showtimes beyond 48 hours | 15–60 minutes |
| Showtimes within 48 hours | 5–10 minutes |
| Auditorium layouts | 1–30 days, keyed by payload hash |
| Live availability | 0–8 seconds |

Redis is the hot cache and concurrency-control surface. PostgreSQL preserves
normalized discovery data, provider observations, and reproducible query
results. The demo runtime uses process memory so it can run without credentials;
production adapters implement the same interfaces and use the durable stores.

## Reliability rules

- Per-provider deadlines and concurrency limits protect the query path.
- Inventory failures degrade individual candidates, not the whole search.
- Idempotency keys bind to canonical request hashes; key reuse with another body
  returns a conflict.
- `GET` results use strong ETags and short private cache headers.
- Provider responses are normalized before transport handlers see them.
- Geometry confidence is part of ranking and the public explanation.
- The final check is read-only. Query traffic never manipulates market
  availability by holding seats.
- JSON logs, health checks, readiness checks, and Prometheus metrics are built
  into the service surface.

## Scale path

The stateless API can be replicated behind a load balancer. Shared idempotency,
query results, and rate-limit leases move to Redis/PostgreSQL when production
providers are enabled. Candidate fan-out is bounded per provider and per tenant,
so horizontal scale does not become an uncontrolled upstream request
multiplier. The normalized schema allows scheduled discovery ingestion to run
separately without changing the query contract.
