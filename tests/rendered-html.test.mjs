import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

async function render() {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}`);
  const previousTimeout = process.env.CENTERSEAT_TRENDING_TIMEOUT_MS;
  process.env.CENTERSEAT_TRENDING_TIMEOUT_MS = "25";

  try {
    const { default: worker } = await import(workerUrl.href);
    return await worker.fetch(
      new Request("http://localhost/", { headers: { accept: "text/html" } }),
      { ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) } },
      { waitUntil() {}, passThroughOnException() {} },
    );
  } finally {
    if (previousTimeout === undefined) delete process.env.CENTERSEAT_TRENDING_TIMEOUT_MS;
    else process.env.CENTERSEAT_TRENDING_TIMEOUT_MS = previousTimeout;
  }
}

test("server-renders the CenterSeat product surface", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /^text\/html\b/i);

  const html = await response.text();
  assert.match(html, /<title>CenterSeat — Find the seat worth booking<\/title>/i);
  assert.match(html, /Find the best seat/);
  assert.match(html, /Popular right now/);
  assert.match(html, /Search movies/);
  assert.match(html, /Via Rotten Tomatoes/);
  assert.doesNotMatch(html, /Live seats connected/);
  assert.doesNotMatch(html, /What should we optimize\?/);
  assert.doesNotMatch(html, /Built for repeatability/);
  assert.doesNotMatch(html, /still open|NOW SHOWING/i);
  assert.doesNotMatch(html, /example\.com|Crown Arc Cinema/);
  assert.doesNotMatch(html, /codex-preview|Your site is taking shape|react-loading-skeleton/i);
});

test("includes accessible controls and the live-query method", async () => {
  const [search, styles, api] = await Promise.all([
    readFile(new URL("../app/components/SearchExperience.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/globals.css", import.meta.url), "utf8"),
    readFile(new URL("../app/lib/api.ts", import.meta.url), "utf8"),
  ]);
  assert.doesNotMatch(search, /lobby-kicker|lobby-assurance/);
  assert.match(search, /aria-label="Best seat query"/);
  assert.match(search, /aria-modal="true"/);
  assert.match(search, /More settings/);
  assert.match(search, /search-schedule-row/);
  assert.match(search, /search-time-row/);
  assert.match(search, /<FieldLabel>Time<\/FieldLabel>/);
  assert.doesNotMatch(search, /<FieldLabel>Time rule<\/FieldLabel>/);
  assert.match(search, /const showStartTime = draft\.timeMode === "inside"/);
  assert.match(search, /const showEndTime = draft\.timeMode === "inside"/);
  assert.match(search, /showStartTime \? <label/);
  assert.match(search, /showEndTime \? <label/);
  assert.match(styles, /\.search-time-row\.time-any\s*\{[\s\S]*?grid-template-columns: 1fr/);
  assert.match(search, /price-ceiling-toggle/);
  assert.match(search, /Seat preference/);
  assert.match(search, /Custom zone/);
  assert.match(search, /custom_seat_zone/);
  assert.match(search, /Check box if required/);
  assert.doesNotMatch(search, /Set the essentials|sensible defaults|Best means/);
  assert.match(search, /Outside this window/);
  assert.match(search, /Wheelchair spaces/);
  assert.match(search, /Audio description/);
  assert.match(search, /The winner gets one final live read/);
  assert.match(search, /Stops early only when no remaining screening can score higher/);
  assert.match(search, /No substitute result was returned/);
  assert.match(search, /height="305"/);
  assert.match(search, /width="206"/);
  assert.match(search, /fetchPriority=\{index < 2/);
  assert.match(styles.match(/\.movie-card-copy \{([\s\S]*?)\}/)?.[1] ?? "", /min-height: 100px/);
  assert.match(styles.match(/\.poster-skeleton > span \{([\s\S]*?)\}/)?.[1] ?? "", /min-height: 100px/);
  assert.match(styles, /\.search-schedule-row[\s\S]*?grid-template-columns:/);
  assert.match(styles, /max-width: 100vw/);
  assert.match(api.match(/createDefaultQuery\(\)[\s\S]*?return \{([\s\S]*?)\n  \};/)?.[1] ?? "", /tickets: 2/);
});

test("uses provider geometry and separates discovery from seat inventory", async () => {
  const [seatMap, search, seatApiRoute, showtimeApiRoute, suggestionsRoute, recommendationRoute, trendingRoute, trendingServer, page] = await Promise.all([
    readFile(new URL("../app/components/SeatMap.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/components/SearchExperience.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/api/seat-queries/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/api/showtime-queries/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/api/movie-suggestions/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/api/seat-recommendations/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/api/trending-movies/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/lib/trending.server.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/page.tsx", import.meta.url), "utf8"),
  ]);

  assert.match(seatMap, /recommendation\.seat_map/);
  assert.match(seatMap, /status-sold/);
  assert.match(seatMap, /status-held/);
  assert.match(seatMap, /geometry-centerline/);
  assert.match(search, /dates: \{ start: draft\.dateStart, end: draft\.dateEnd \}/);
  assert.match(search, /hasPostalLocation/);
  assert.doesNotMatch(search, /Search depth|Fast · 6 maps|candidate_limit: draft\.candidateLimit/);
  assert.match(search, /expands automatically/);
  assert.match(search, /movie_id/);
  assert.match(search, /movie-suggestions/);
  assert.match(search, /seat-recommendations/);
  assert.match(search, /trending-movies/);
  assert.match(search, /centerseat\.preferences\.v1/);
  assert.match(search, /winner_verified/);
  assert.match(seatMap, /recommended_zone/);
  assert.match(seatMap, /preferred_depth/);
  assert.match(seatMap, /preferred_zone_bounds/);
  assert.match(seatMap, /seat_options/);
  assert.match(seatMap, /Strong alternative/);
  assert.match(search, /range_best_proven/);
  assert.match(search, /BEST ACROSS RANGE/);
  assert.match(search, /BEST MATCH/);
  assert.match(search, /recommendationChoices/);
  assert.match(search, /loadedRecommendations/);
  assert.match(search, /discovery_ms/);
  assert.match(search, /\/api\/seat-queries/);
  assert.match(search, /\/api\/showtime-queries/);
  assert.match(search, /Showtimes only/);
  assert.match(seatApiRoute, /CENTERSEAT_API_URL/);
  assert.match(seatApiRoute, /45_000/);
  assert.match(seatApiRoute, /Live inventory search took too long/);
  assert.match(showtimeApiRoute, /queryOpenCinema/);
  assert.match(suggestionsRoute, /movie-suggestions/);
  assert.match(recommendationRoute, /recommendations/);
  assert.match(trendingServer, /cnapi\/browse\/movies_in_theaters\/sort:popular/);
  assert.match(trendingServer, /cache: "force-cache"/);
  assert.match(trendingRoute, /stale-while-revalidate=21600/);
  assert.match(trendingServer, /unavailablePayload/);
  assert.match(page, /initialTrending=\{trending\.movies\}/);
  const layout = await readFile(new URL("../app/layout.tsx", import.meta.url), "utf8");
  assert.match(layout, /\/icon\.png/);
  assert.doesNotMatch(search, /runDemoQuery|example\.com|Crown Arc Cinema/);
});
