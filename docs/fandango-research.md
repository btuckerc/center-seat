# Fandango local read-only adapter and research bookmark

Status: **implemented behind an explicit local-only mode and not a hosted
production dependency**.

This document preserves the anonymous, read-only protocol observed by the
MIT-licensed `atfinke/fandango-mcp` project and provides a safe capture workflow
for personal research. The opt-in local adapter calls only the three allowlisted
GET routes documented below. It does not enter ticketing, replay captured
requests, or circumvent access controls.

## Observed protocol

Movie showtimes near a location:

```http
GET https://www.fandango.com/napi/theaterShowtimeGroupings/{movieId}/{date}
    ?lat={latitude}&long={longitude}&isdesktop=true&isDesktopMOP=true
```

The response nests theaters, format variants, showtimes, provider checkout
links, and a `showtimeHashCode` used by the read-only seat-map preview.

Seat-map preview:

```http
GET https://www.fandango.com/napi/seatMap/{showtimeHashCode}
```

Observed seat records include `id`, `row`, `column`, `x`, `y`, `type`, and
`status`. The open-source normalizer treats `A` as available and `R` as
reserved/unavailable. Additional theater-specific values must fail closed until
captured and understood.

Other discovery routes preserved for later investigation:

```http
GET https://www.fandango.com/napi/theaterMovieShowtimes/{theaterId}
GET https://www.fandango.com/napi/theaterCalendar/{theaterId}
```

Observed browser requests used ordinary JSON/XHR headers and required no
account, checkout, order, hold, or payment call.

Movie title resolution:

```http
GET https://www.fandango.com/napi/home/autocompleteDesktopSearch
    ?search={movie title}
```

The response groups results by type. Movie items supply the Fandango movie ID,
canonical link/slug, name, release date, and poster metadata. This completes a
free-form-title-to-seat-map read path without a login or checkout session.

## Local implementation

Set `CENTERSEAT_ENV=local` and
`CENTERSEAT_PROVIDER_MODE=fandango-local`. The adapter:

- rejects startup in `production`;
- permits only autocomplete, theater-showtime grouping, and seat-map GETs;
- rejects redirects so an allowlisted read cannot cross into ticketing;
- never sends cookies, authorization, CSRF, or checkout-session values;
- spaces request starts and caps concurrency;
- caches title resolution, showtime discovery, and two-second inventory reads;
- maps observed `A`, `R`, and `H` states to available, sold, and held,
  while every unknown state fails closed;
- normalizes seat centers against the global auditorium seat bounds rather than
  independently stretching each row; and
- bypasses the cache for the final winning-seat verification.

The captured click-through sequence confirmed that
`POST /checkoutapi/reservations/v2` creates reservation state before purchase
and can return conflicts or server errors. That route and all token, wallet,
payment, cart, and ticketing-service calls are intentionally absent from the
adapter.

## Constraint

Fandango's current Terms of Use prohibit automated extraction, and its
`robots.txt` disallows `/napi/*`. The existence of anonymous routes or
open-source client code does not grant permission to operate a hosted scraper.
The adapter therefore remains opt-in and local-only unless Fandango authorizes
a hosted use. Enabling it means the owner deliberately accepts a personal-use
posture after reviewing the current terms.

- Terms: https://www.fandango.com/policies/terms-of-use
- Robots: https://www.fandango.com/robots.txt
- Reference implementation: https://github.com/atfinke/fandango-mcp

## User-driven capture workflow

1. Open the browser's developer tools and select **Network**.
2. Enable **Preserve log** and filter to **Fetch/XHR**.
3. Use Fandango normally: search for a movie, choose a date/theater/showtime,
   and open the seat-map preview. Do not add tickets, select seats, hold seats,
   sign in, or enter checkout.
4. Export the network log as a HAR file **with sensitive data removed** when the
   browser offers that option.
5. Keep the HAR local and run:

   ```bash
   npm run research:fandango-har -- /absolute/path/to/capture.har
   ```

The analyzer prints only request method/host/path, query-parameter names,
status/content type, and bounded JSON shape metadata. It never prints request
headers, cookies, authorization values, response values, or full URLs, and it
does not replay any request. HAR files can still contain sensitive material;
delete the original after the useful protocol facts have been recorded here.

## Hosted enablement bar

Before any hosted implementation is enabled, obtain authorization and re-review
the routes. The current local adapter already documents observed status values,
uses contract fixtures, enforces a GET-only path allowlist, caps request volume,
and refuses ticketing paths.
