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
  assert.match(html, /Find the one/);
  assert.match(html, /What should we optimize\?/);
  assert.match(html, /Date range starts/);
  assert.match(html, /Range ends/);
  assert.match(html, /Provider setup required|Checking live providers/);
  assert.match(html, /Results appear only after configured live providers respond/);
  assert.match(html, /Advanced constraints/);
  assert.match(html, /Open Cinema API key/);
  assert.match(html, /Use precise location · required/);
  assert.doesNotMatch(html, /example\.com|Crown Arc Cinema|Spider-Man: Brand New Day/);
  assert.doesNotMatch(html, /codex-preview|Your site is taking shape|react-loading-skeleton/i);
});

test("includes accessible controls and the live-query method", async () => {
  const html = await (await render()).text();
  assert.match(html, /aria-label="Best seat query"/);
  assert.match(html, /Outside this window/);
  assert.match(html, /Wheelchair spaces/);
  assert.match(html, /Audio description/);
  assert.match(html, /Verify the winner/);
  assert.match(html, /No demo fallback\. No holds\./);
  assert.match(html, /refusing to show fabricated results/);
});

test("uses provider geometry and separates discovery from seat inventory", async () => {
  const [seatMap, search, seatApiRoute, showtimeApiRoute] = await Promise.all([
    readFile(new URL("../app/components/SeatMap.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/components/SearchExperience.tsx", import.meta.url), "utf8"),
    readFile(new URL("../app/api/seat-queries/route.ts", import.meta.url), "utf8"),
    readFile(new URL("../app/api/showtime-queries/route.ts", import.meta.url), "utf8"),
  ]);

  assert.match(seatMap, /recommendation\.seat_map/);
  assert.match(seatMap, /status-sold/);
  assert.match(seatMap, /status-held/);
  assert.match(seatMap, /geometry-centerline/);
  assert.match(search, /dates: \{ start: draft\.dateStart, end: draft\.dateEnd \}/);
  assert.match(search, /draft\.latitude === undefined/);
  assert.match(search, /\/api\/seat-queries/);
  assert.match(search, /\/api\/showtime-queries/);
  assert.match(search, /Discovery-only/);
  assert.match(seatApiRoute, /CENTERSEAT_API_URL/);
  assert.match(showtimeApiRoute, /queryOpenCinema/);
  assert.doesNotMatch(search, /runDemoQuery|example\.com|Crown Arc Cinema/);
});
