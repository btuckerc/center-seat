import assert from "node:assert/strict";
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
