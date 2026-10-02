# Architecture

CenterSeat has two application services:

- a web app for search and seat-map display;
- a Go API for discovery, inventory reads, filtering, and ranking.

Provider adapters sit behind one normalized API; query results are held in
in-process memory only and are lost on restart.

## Query path

```text
search request
  -> provider showtimes
  -> local constraint filtering
  -> parallel seat-map reads
  -> contiguous-seat ranking
  -> final availability check
  -> winner and alternatives
```

The service spreads its first inventory checks across the requested dates. It
continues while an unchecked screening can still beat the current winner. A
result is marked partial when a competitive screening could not be checked.

Alternatives are returned without full maps. Opening one refreshes only that
screening.

Snapshots expire at `expires_at`; results remain available for recommendation
refreshes until `refresh_until` (15 minutes after generation). Reads after
snapshot expiry return HTTP 410. The in-memory cache is capped at 1,000 results
and evicts them at the refresh deadline.

Query times and displayed screening times use the explicit query IANA timezone.
Provider wall-clock screening times may be interpreted using each venue's
optional IANA timezone.

## Ranking

The ranker uses provider coordinates when available. It scores complete party
blocks, not individual seats, and supports:

- balanced;
- dead center;
- two-thirds back;
- aisle, front, or back;
- a normalized custom area.

If no seat exists in a preferred area, the response labels the nearest fallback.
The final check reads inventory again and promotes the next result if needed.

## Boundaries

- Provider calls have timeouts, retries, and concurrency limits.
- Unknown seat states are unavailable.
- Inventory failures affect one screening instead of failing the whole search.
- Identical in-flight seat-map requests are shared.
- Query traffic never creates a hold or reservation.
- Provider credentials stay in the API environment.

## Deployment

The Mac mini runs the Compose stack. Only the web service is bound to loopback;
Cloudflare Tunnel publishes it at [movies.angl.gg](https://movies.angl.gg).

See [deploy/macmini/README.md](../deploy/macmini/README.md) for commands.
