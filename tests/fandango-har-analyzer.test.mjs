import assert from "node:assert/strict";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { execFile } from "node:child_process";
import test from "node:test";

const execFileAsync = promisify(execFile);

test("HAR analyzer reports shapes without leaking request secrets or response values", async () => {
  const directory = await mkdtemp(join(tmpdir(), "centerseat-har-"));
  const capture = join(directory, "capture.har");
  await writeFile(capture, JSON.stringify({ log: { entries: [{
    request: {
      method: "GET",
      url: "https://www.fandango.com/napi/seatMap/hash-secret?token=do-not-print",
      headers: [{ name: "Cookie", value: "session=do-not-print" }],
    },
    response: {
      status: 200,
      content: { mimeType: "application/json", text: JSON.stringify({ seats: [{ id: "A1", status: "A" }], privateValue: "do-not-print" }) },
    },
  }] } }));
  const { stdout } = await execFileAsync(process.execPath, ["scripts/analyze-fandango-har.mjs", capture], { cwd: process.cwd() });
  assert.match(stdout, /read-only seat-map preview/);
  assert.match(stdout, /query_parameter_names/);
  assert.match(stdout, /privateValue/);
  assert.doesNotMatch(stdout, /hash-secret|do-not-print|session=/);
});

test("HAR flow mode traces a hold sequence without leaking personal data or credentials", async () => {
  const directory = await mkdtemp(join(tmpdir(), "centerseat-har-"));
  const capture = join(directory, "flow.har");
  const cartID = "cart-7f3a9c21e4b84d0f9a6b2c5d";
  const entry = (seconds, method, url, request, response) => ({
    startedDateTime: new Date(Date.UTC(2026, 9, 2, 0, 0, seconds)).toISOString(),
    _resourceType: "fetch",
    request: { method, url, headers: request.headers ?? [], postData: request.postData },
    response: { status: 200, headers: response.headers ?? [], content: response.content ?? {} },
  });
  // Entries are deliberately out of order; the report must be chronological.
  await writeFile(capture, JSON.stringify({ log: { entries: [
    entry(30, "DELETE", `https://tickets.fandango.com/api/cart/${cartID}`, { headers: [{ name: "X-CSRF-Token", value: "csrf-91b2c3d4e5f6a7b8c9d0e1f2" }] }, {}),
    entry(10, "POST", "https://tickets.fandango.com/api/reserve", {
      headers: [{ name: "Cookie", value: "sid=session-do-not-print; geo=US" }, { name: "X-CSRF-Token", value: "csrf-91b2c3d4e5f6a7b8c9d0e1f2" }],
      postData: { mimeType: "application/json", text: JSON.stringify({ seats: ["E7", "E8"], quantity: 2, email: "person@example.com" }) },
    }, {
      headers: [{ name: "Set-Cookie", value: "hold=secret-do-not-print; Path=/" }],
      content: { mimeType: "application/json", text: JSON.stringify({ cartId: cartID, holdSeconds: 600, customer: { firstName: "Private" } }) },
    }),
    entry(5, "GET", "https://tickets.fandango.com/robots.txt?centerseat_phase=reserve", {}, {}),
    entry(1, "GET", "https://tickets.fandango.com/static/app.js", {}, {}),
    entry(2, "GET", "https://payments.example.net/frame", {}, {}),
  ] } }));
  const { stdout } = await execFileAsync(process.execPath, ["scripts/analyze-fandango-har.mjs", "--flow", capture], { cwd: process.cwd() });
  const report = JSON.parse(stdout);
  assert.doesNotMatch(stdout, /do-not-print|person@example\.com|Private|cart-7f3a|csrf-91b2/);
  assert.deepEqual(report.steps.map((step) => step.phase ?? step.method), ["reserve", "POST", "DELETE"]);
  assert.deepEqual(report.other_hosts, { "payments.example.net": 1 });
  const [, reserve, release] = report.steps;
  assert.deepEqual(reserve.request_body.json.seats, ["E7", "E8"]);
  assert.equal(reserve.request_body.json.quantity, 2);
  assert.deepEqual(reserve.cookie_names, ["geo", "sid"]);
  assert.deepEqual(reserve.set_cookie_names, ["hold"]);
  // The cart ID minted by the hold must be traceable into the release path, and the CSRF value reused.
  assert.ok(release.path.endsWith(`/${reserve.response_body.json.cartId}`));
  assert.equal(release.credential_headers["X-CSRF-Token"], reserve.credential_headers["X-CSRF-Token"]);
});
