# Agent interfaces

CenterSeat exposes the same read-only seat search to software agents in two
forms: four JSON routes under `/api/agent/*` and an MCP endpoint at
`/api/mcp`. Both are served by the web app and call the Go API. They never
hold, reserve or buy tickets.

## Access policy

| Routes | Auth | Purpose |
| --- | --- | --- |
| `/api/agent/search`, `/api/agent/recommendation`, `/api/agent/movies`, `/api/agent/providers`, `/api/mcp` | `Authorization: Bearer <CENTERSEAT_AGENT_TOKEN>` | Programmatic agent access |
| `/api/seat-queries`, `/api/seat-recommendations`, `/api/showtime-queries`, `/api/discovery-prefetch`, `/api/movie-suggestions`, `/api/providers`, `/api/trending-movies` | None (public) | The browser UI |

- The agent routes are switched off (`404`) until `CENTERSEAT_AGENT_TOKEN` is
  set. A missing or wrong token gets `401 unauthorized`.
- The token's scope is all agent JSON routes and every MCP tool. It is one
  shared secret: there are no per-agent tokens or per-tool scopes. Rotating it
  revokes every agent at once.
- The browser routes are deliberately public and unauthenticated so the web UI
  works without sign-in. They reach the same read-only backend, so the token
  gates the agent-shaped interface, not the underlying data. Restricting the
  browser routes would be a policy change; it has not been made.
- `/api/mcp` also rejects requests whose `Origin` header differs from
  `CENTERSEAT_PUBLIC_URL`, so a web page cannot drive it from a browser.

## Managing the credential

The token lives only in the production `.env` on the Mac mini
(`/Users/admin/src/centerseat/.env`, mode 600). Use
`deploy/macmini/agent-token.sh`; it never prints the value to a terminal.

```sh
# Is a token set? Shows a short SHA-256 fingerprint, never the value.
ssh mini 'cd ~/src/centerseat && deploy/macmini/agent-token.sh status'

# Replace it and restart the web container. Every agent must be updated.
ssh mini 'cd ~/src/centerseat && deploy/macmini/agent-token.sh rotate'

# Copy it straight to the clipboard on your Mac, then paste into the agent's
# secret field. `print` refuses to write to a terminal.
ssh mini 'cd ~/src/centerseat && deploy/macmini/agent-token.sh print' | pbcopy
```

Never put the token in URLs, chat messages, prompts, issue trackers or logs.
Agents should keep it in their secret store (a Custom GPT Action's API key
field, or an environment variable referenced by an MCP client config).

### ChatGPT (Custom GPT Action)

1. GPT editor → Configure → Actions → Create new action.
2. Authentication: API Key, Auth Type Bearer; paste from the clipboard.
3. Schema: paste [`openapi/agent.yaml`](../openapi/agent.yaml).

ChatGPT's MCP apps sign in with OAuth or no authentication, and agent mode
does not use custom apps, so use the Action for ChatGPT.

### MCP clients that support headers

Point the client at `https://movies.angl.gg/api/mcp` with
`Authorization: Bearer $CENTERSEAT_AGENT_TOKEN`, reading the variable from the
client's environment rather than writing the value into its config file.

## JSON routes

All responses are `Cache-Control: no-store`.

### `POST /api/agent/search`

```json
{
  "movie": "Primetime",
  "movie_id": "245965",
  "timezone": "America/New_York",
  "location": { "query": "Brooklyn, NY" },
  "dates": { "start": "2026-10-03", "end": "2026-10-04" },
  "tickets": 2
}
```

Required: `movie`, `timezone` (IANA), `location`. `location` takes `zip`, free
text `query` (city or address), or both `latitude` and `longitude`. Optional:
`movie_id` (from `/api/agent/movies`; skips title matching), `dates`,
`tickets` (1–8), `profile`, `time {mode,start,end}`, `formats`,
`max_distance_miles`, `max_total_price`, `captions`, `accessibility`,
`exclude_first_rows`, `recliners`, `allow_split_party`.

The response has `status`, `query_id`, `movie_id`, `expires_at`,
`refresh_until`, `resolved_location`, `share_url`, `coverage`, `warnings`,
`winner` and `alternatives`. Without live seat inventory it returns
`showtimes` instead of `winner`/`alternatives`.

- `resolved_location` (`label`, `latitude`, `longitude`) is present when the
  backend turned free text into coordinates. Check the label; if a city name
  is ambiguous, search again with a ZIP.
- Each recommendation has `rank`, `venue`, `local_start`, `format`, `seats`,
  `verified_at`, `booking_url` and `price`. The winner also has `handoff`.
- `price` is an estimate, never a final checkout total: `ticket_count`,
  `currency`, `ticket_price` (per ticket), `fee_per_ticket`,
  `estimated_total`, `fees_included`, `is_estimate`, `qualification`. `null`
  means unknown; `fee_per_ticket: 0` means known to have no fee, while `null`
  means the fee is unknown and not included.
- `share_url` reproduces the search in the browser with the same location,
  dates, party size, preferences and explicit timezone.

### `GET /api/agent/recommendation?query_id=…&rank=1&tz=America%2FNew_York`

Re-reads live inventory for one rank and returns the same recommendation
shape plus `handoff`, with a new `verified_at`. Call this before acting on any
result.

### `GET /api/agent/movies?q=…`

Title suggestions with provider `id`, `title` and `year`. Pass the chosen `id`
as `movie_id` to search that exact movie.

### `GET /api/agent/providers`

Provider health and `location_mode`.

## MCP

Streamable HTTP, JSON-RPC 2.0 over `POST https://movies.angl.gg/api/mcp`
(responses are plain JSON; `GET` and `DELETE` return 405). Supported protocol
versions: `2025-06-18`, `2025-03-26`. Methods: `initialize`, `ping`,
`tools/list`, `tools/call`; notifications get `202`.

| Tool | Equivalent route |
| --- | --- |
| `search_seats` | `POST /api/agent/search` |
| `refresh_recommendation` (`query_id`, `rank`, `timezone`) | `GET /api/agent/recommendation` |
| `suggest_movies` (`q`) | `GET /api/agent/movies` |
| `list_providers` | `GET /api/agent/providers` |

Tool results carry both `structuredContent` and a JSON text block. A failed
tool call returns a normal result with `isError: true` and
`{ code, status, message }` using the codes below. Protocol errors use
JSON-RPC codes (`-32700`, `-32600`, `-32601`, `-32602`).

## Freshness and handoff

- `expires_at`: the search snapshot is good for about 8 seconds.
- `refresh_until`: ranks can be re-verified with the recommendation route
  until then (about 15 minutes); afterwards run the search again.
- `verified_at` is the time of the live inventory read behind that result.
- Handoff names the exact seats, ticket count, venue, local start with
  timezone, booking URL and price estimate. Select exactly those seats, never
  substitute, and stop before payment.

## Errors

JSON routes return RFC 9457 problem JSON
(`application/problem+json`) with a stable `code`:

| Status | `code` | Meaning / action |
| --- | --- | --- |
| 400 | `invalid_request` | Malformed request; fix it |
| 401 | `unauthorized` | Missing or wrong bearer token |
| 404 | — | Agent routes disabled (no token configured) |
| 403 | `origin_forbidden` | MCP request from a foreign `Origin` |
| 409 | `seats_unavailable`, `screening_started`, `price_exceeded` | That rank is no longer valid; try another rank or search again |
| 410 | `query_expired` | Snapshot or refresh window over; search again |
| 422 | `validation_failed` | Request is well-formed but invalid, including a location that cannot be found |
| 503 | `provider_unavailable` | Upstream provider or geocoder unavailable; retry later |
