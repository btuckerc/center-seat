import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

async function render() {
  const workerUrl = new URL("../dist/server/index.js", import.meta.url);
  workerUrl.searchParams.set("test", `${process.pid}-${Date.now()}`);
  const { default: worker } = await import(workerUrl.href);

  return worker.fetch(
    new Request("http://localhost/", { headers: { accept: "text/html" } }),
    { ASSETS: { fetch: async () => new Response("Not found", { status: 404 }) } },
    { waitUntil() {}, passThroughOnException() {} },
  );
}

test("server-renders the CenterSeat product surface", async () => {
  const response = await render();
  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /^text\/html\b/i);

  const html = await response.text();
  assert.match(html, /<title>CenterSeat — Find the seat worth booking<\/title>/i);
  assert.match(html, /The best seat/);
  assert.match(html, /Popular right now/);
  assert.match(html, /Search any movie/);
  assert.match(html, /Source required|Checking live sources/);
  assert.match(html, /Via Rotten Tomatoes/);
  assert.doesNotMatch(html, /What should we optimize\?/);
  assert.doesNotMatch(html, /Built for repeatability/);
  assert.doesNotMatch(html, /example\.com|Crown Arc Cinema|Spider-Man: Brand New Day/);
  assert.doesNotMatch(html, /codex-preview|Your site is taking shape|react-loading-skeleton/i);
});

test("includes accessible controls and the live-query method", async () => {
  const search = await readFile(new URL("../app/components/SearchExperience.tsx", import.meta.url), "utf8");
  assert.match(search, /aria-label="Best seat query"/);
  assert.match(search, /aria-modal="true"/);
  assert.match(search, /Fine-tune the search/);
  assert.match(search, /Outside this window/);
  assert.match(search, /Wheelchair spaces/);
  assert.match(search, /Audio description/);
  assert.match(search, /The winner gets one final live read/);
  assert.match(search, /No substitute result was returned/);
});

test("uses provider geometry and separates discovery from seat inventory", async () => {
  const [seatMap, search, seatApiRoute, showtimeApiRoute, suggestionsRoute, recommendationRoute, trendingRoute] = await Promise.all([
    readFile(new URL("../app/components/SeatMap.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/components/SearchExperience.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/api/seat-queries/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/api/showtime-queries/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/api/movie-suggestions/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/api/seat-recommendations/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/api/trending-movies/route.ts", import.meta.url), "utf8"),
  ]);

  assert.match(seatMap, /recommendation\.seat_map/);
  assert.match(seatMap, /status-sold/);
  assert.match(seatMap, /status-held/);
  assert.match(seatMap, /geometry-centerline/);
  assert.match(search, /dates: \{ start: draft\.dateStart, end: draft\.dateEnd \}/);
  assert.match(search, /hasPostalLocation/);
  assert.match(search, /Live source warming up/);
  assert.doesNotMatch(search, /Search depth|Fast · 6 maps|candidate_limit: draft\.candidateLimit/);
  assert.match(search, /expands automatically when needed/);
  assert.match(search, /movie_id/);
  assert.match(search, /movie-suggestions/);
  assert.match(search, /seat-recommendations/);
  assert.match(search, /trending-movies/);
  assert.match(search, /centerseat\.preferences\.v1/);
  assert.match(search, /winner_verified/);
  assert.match(seatMap, /recommended_zone/);
  assert.match(seatMap, /seat_options/);
  assert.match(search, /\/api\/seat-queries/);
  assert.match(search, /\/api\/showtime-queries/);
  assert.match(search, /Showtimes only/);
  assert.match(seatApiRoute, /CENTERSEAT_API_URL/);
  assert.match(showtimeApiRoute, /queryOpenCinema/);
  assert.match(suggestionsRoute, /movie-suggestions/);
  assert.match(recommendationRoute, /recommendations/);
  assert.match(trendingRoute, /cnapi\/browse\/movies_in_theaters\/sort:popular/);
  assert.match(trendingRoute, /movies: \[\]/);
  assert.doesNotMatch(search, /runDemoQuery|example\.com|Crown Arc Cinema/);
});
