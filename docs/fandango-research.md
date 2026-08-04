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
```

The analyzer reports methods, hosts, paths, parameter names, response types, and
JSON shapes. It does not print headers, cookies, response values, or full URLs,
and it does not replay requests. HAR files may still contain private data; keep
them outside the repository and delete them when finished.

## Limits

Fandango's terms and `robots.txt` restrict automated access. Anonymous routes
are not permission to run a hosted scraper. Review both before enabling this
adapter for personal use:

- [Terms of Use](https://www.fandango.com/policies/terms-of-use)
- [robots.txt](https://www.fandango.com/robots.txt)

The adapter must remain local unless Fandango authorizes another use.
