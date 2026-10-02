# Fandango local adapter

This adapter is for personal, read-only use. It runs only with:

```dotenv
CENTERSEAT_ENV=local
CENTERSEAT_PROVIDER_MODE=fandango-local
```

It does not sign in, hold seats, create a cart, or enter checkout.

## Read routes

```http
GET /napi/home/autocompleteDesktopSearch?search={title}
GET /napi/theaterShowtimeGroupings/{movieId}/{date}?lat={lat}&long={long}&isdesktop=true&isDesktopMOP=true
GET /napi/seatMap/{showtimeHashCode}
```

The adapter has a fixed GET allowlist, rejects redirects, sends no cookies or
authorization headers, limits concurrency, and performs an uncached final read
for the winner. Unknown seat states are unavailable.

The implementation intentionally excludes the reservation and checkout routes
observed in browser traffic.

## Inspecting a HAR

Export a sanitized HAR after using Fandango normally, then run:

```sh
npm run research:fandango-har -- /absolute/path/to/capture.har
npm run research:fandango-har -- --flow /absolute/path/to/capture.har
```

The default report lists methods, hosts, paths, parameter names, response
types, and JSON shapes. `--flow` is a chronological transcript of the
non-static Fandango requests for studying the seat-select → hold → release
sequence: request and response bodies with personal fields replaced by
`<personal>`, credentials and opaque identifiers replaced by stable aliases
(`<v3 len=36>`, the same value always gets the same alias so a cart ID or CSRF
token can be traced from the response that minted it to later requests),
header and cookie names without values, HTML form fields, and the hostnames of
any non-Fandango requests. Neither mode replays requests. HAR files still
contain private data; keep them outside the repository and delete them when
finished.

### Capturing a hold flow in Chrome

1. Open a CenterSeat result and press **Continue at provider** but don't touch
   the Fandango tab yet. Open DevTools (`⌥⌘I`) → **Network**; tick
   **Preserve log** and **Disable cache**; leave the filter on **All**.
2. Reload the Fandango tab. Mark each phase from the DevTools **Console** so
   the transcript is segmented, e.g.
   `fetch('/robots.txt?centerseat_phase=select')`, then `reserve`,
   `change`, `release`.
3. Pick the ticket count, select the seats, and continue until Fandango shows
   a hold timer or the payment page. Never enter payment details.
4. Go back and choose different seats, continue again, then leave via
   **Cancel**/**Start over** (or the back button) so a release is captured.
5. Network toolbar → download icon → **Export HAR (sanitized)…**. The
   sanitized export omits `Cookie`, `Set-Cookie`, and `Authorization`.

Pages that navigate can drop earlier response bodies from DevTools; export
promptly after the flow, and repeat the capture if a step shows no body.

## Limits

Fandango's terms and `robots.txt` restrict automated access. Anonymous routes
are not permission to run a hosted scraper. Review both before enabling this
adapter for personal use:

- [Terms of Use](https://www.fandango.com/policies/terms-of-use)
- [robots.txt](https://www.fandango.com/robots.txt)

The adapter must remain local unless Fandango authorizes another use.
