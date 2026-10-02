# Architecture

CenterSeat has two application services:

- a web app for search and seat-map display;
- a Go API for discovery, inventory reads, filtering, and ranking.

Provider adapters sit behind one normalized API; query results are held in
in-process memory only and are lost on restart.

## Query path

```text
search request
  -> resolve free-text locations once in the service (when configured)
  -> provider showtimes
  -> local constraint filtering
  -> parallel seat-map reads
  -> contiguous-seat ranking
  -> final availability check
  -> winner and alternatives
```

Seat-map reads run as a continuous pipeline: each provider slot is refilled as
soon as a read finishes. A screening is read only while it can still matter: it
could outrank the current leader (including score ties the ordering would break
in its favor), its date still needs coverage, or fewer than five results exist
to fill the winner and alternatives. If the final check demotes the winner,
skipped screenings that could beat the new leader are read and any new leader is
checked again. A result is marked partial when a competitive screening could not
be checked.

While the search sheet is being filled in, the web app asks the API to prefetch
discovery (`POST /v1/discovery-prefetch`) for the composed movie, place, dates,
and timezone. The Fandango adapter keeps showtime listings for two minutes and
shares identical in-flight listing reads, so the search usually starts at seat
maps. Prefetch never reads seat maps; the API admits one at a time and limits
its upstream reads so real searches keep priority. Movie suggestions are cached
for ten minutes. The home page renders provider status and trending titles on
the server; trending is served stale-while-revalidate for up to a day.

Alternatives are returned without full maps. Opening one refreshes only that
screening.

Snapshots expire at `expires_at`; results remain available for recommendation
refreshes until `refresh_until` (15 minutes after generation). Reads after
snapshot expiry return HTTP 410. The in-memory cache is capped at 1,000 results
and evicts them at the refresh deadline.

Query times and displayed screening times use the explicit query IANA timezone.
Provider wall-clock screening times may be interpreted using each venue's
optional IANA timezone.

## Locations

The Go service resolves free-text locations (ZIPs, cities, neighborhoods,
street addresses) to coordinates once, before discovery, so coordinate-only
providers work with any of them. The original text is kept for providers that
prefer a postal code; if the geocoder fails and the text contains a ZIP, the
search continues on the ZIP alone. A place that cannot be found returns `422
validation_failed`. Responses include `resolved_location` when geocoding was
used so clients can confirm the matched place; ambiguous city names should be
searched by ZIP.

The resolver speaks the Nominatim search API (public OSM by default;
`CENTERSEAT_GEOCODER_URL` can point at a self-hosted or compatible service,
`off` disables it). It caches hits for 30 days and misses for 10 minutes,
coalesces identical in-flight lookups, and sends at most one upstream request
per second, per the public service's usage policy.

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

- Provider calls have timeouts, retries, and concurrency limits. A concurrency
  slot is held until the response body is fully read.
- An HTTP 429 pauses every new provider request until `Retry-After` (or a
  jittered backoff); a request whose deadline ends first fails right away.
- Unknown seat states are unavailable.
- Inventory failures affect one screening instead of failing the whole search.
- Identical in-flight seat-map and showtime-listing requests are shared. A
  shared seat-map read is cancelled only when every search waiting on it has
  left; final verification reads never join one.
- Query traffic never creates a hold or reservation.
- Provider credentials stay in the API environment.

## Deployment

The Mac mini runs the Compose stack. Only the web service is bound to loopback;
Cloudflare Tunnel publishes it at [movies.angl.gg](https://movies.angl.gg).

See [deploy/macmini/README.md](../deploy/macmini/README.md) for commands.
