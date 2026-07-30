# Fandango read-only research bookmark

Status: **disabled and not a production dependency**.

This document preserves the anonymous, read-only protocol observed by the
MIT-licensed `atfinke/fandango-mcp` project and provides a safe capture workflow
for personal research. CenterSeat does not call these routes, replay captured
requests, enter ticketing, or circumvent access controls.

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

## Constraint

Fandango's current Terms of Use prohibit automated extraction, and its
`robots.txt` disallows `/napi/*`. The existence of anonymous routes or
open-source client code does not grant permission to operate a hosted scraper.
This bookmark therefore remains research-only unless Fandango authorizes the
use or the owner deliberately accepts a local personal-use posture after
reviewing the current terms.

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

## Enablement bar

Before any implementation is enabled, verify the route still works through
ordinary user navigation, document every status value, add contract fixtures,
enforce GET-only allowlists, cap request volume, refuse ticketing hosts and
paths, and keep the adapter local-only unless authorization changes.
