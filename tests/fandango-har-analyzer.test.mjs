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
