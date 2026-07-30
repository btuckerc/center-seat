import assert from "node:assert/strict";
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
  assert.match(html, /Spider-Man: Brand New Day/);
  assert.match(html, /Best available/i);
  assert.match(html, /Advanced constraints/);
  assert.doesNotMatch(html, /codex-preview|Your site is taking shape|react-loading-skeleton/i);
});

test("includes accessible controls and the live-query method", async () => {
  const html = await (await render()).text();
  assert.match(html, /aria-label="Best seat query"/);
  assert.match(html, /Outside this window/);
  assert.match(html, /Wheelchair spaces/);
  assert.match(html, /Audio description/);
  assert.match(html, /Verify the winner/);
  assert.match(html, /No holds\. No phantom availability\./);
});
