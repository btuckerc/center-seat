import assert from "node:assert/strict";
import test from "node:test";
import { createDefaultQuery, dateInTimeZone, queryStateToRequest } from "../app/lib/api.ts";
import { queryFromSearchParams, searchParamsFromQuery, shareURL } from "../app/lib/share.ts";

test("a shared search round-trips every constraint, including explicit place and timezone", () => {
  const state = {
    ...createDefaultQuery("America/Los_Angeles"),
    movie: "Dune: Part Two",
    movieId: "movie-42",
    location: "90028",
    dateStart: "2026-10-03",
    dateEnd: "2026-10-05",
    tickets: 3,
    timeMode: "inside",
    startTime: "22:00",
    endTime: "01:00",
    profile: "custom",
    customSeatZone: { minimumX: 0.2, maximumX: 0.8, minimumY: 0.4, maximumY: 0.7 },
    formats: ["IMAX", "Dolby"],
    maxDistance: 12,
    limitPrice: true,
    maxPrice: 85,
    captions: "closed",
    audioDescription: true,
    wheelchairSpaces: 1,
    companionSeats: 1,
    excludeFirstRows: 2,
    recliners: true,
    allowSplit: true,
  };
  const parsed = queryFromSearchParams(new URL(shareURL("https://movies.angl.gg", state)).searchParams);
  assert.deepEqual(parsed.errors, []);
  assert.equal(parsed.run, false);
  assert.deepEqual({ ...createDefaultQuery("UTC"), ...parsed.query }, state);
  assert.equal(queryStateToRequest({ ...createDefaultQuery("UTC"), ...parsed.query }).time.timezone, "America/Los_Angeles");
});

test("coordinates survive a link and the link never depends on the opener's zone", () => {
  const state = { ...createDefaultQuery("Europe/Berlin"), movie: "Heat", location: "Current location", latitude: 52.52, longitude: 13.405 };
  const params = searchParamsFromQuery(state, { run: true });
  const parsed = queryFromSearchParams(params);
  assert.equal(parsed.query.timezone, "Europe/Berlin");
  assert.equal(parsed.query.latitude, 52.52);
  assert.equal(parsed.query.longitude, 13.405);
  assert.equal(parsed.run, true);
});

test("malformed fields are reported instead of silently dropped", () => {
  const parsed = queryFromSearchParams(new URLSearchParams("v=1&movie=Heat&tz=Mars/Base&from=2026-13-40&to=2026-10-01&tickets=0&lat=40&profile=sideways&start=25:00"));
  assert.deepEqual(parsed.query, { movie: "Heat", dateEnd: "2026-10-01" });
  assert.equal(parsed.errors.length, 6);
  assert.ok(parsed.errors.some((error) => error.startsWith("tz ")));
  assert.ok(parsed.errors.some((error) => error.startsWith("lat and lon")));
});

test("a page without a shared search is not treated as one", () => {
  assert.equal(queryFromSearchParams(new URLSearchParams("utm_source=x")), null);
});

test("run is opt-in and malformed or unknown link parameters are reported", () => {
  const state = { ...createDefaultQuery("Europe/Berlin"), movie: "Heat", movieId: "canonical-7" };
  assert.equal(queryFromSearchParams(searchParamsFromQuery(state)).run, false);
  assert.equal(queryFromSearchParams(searchParamsFromQuery(state, { run: true })).run, true);
  const parsed = queryFromSearchParams(new URLSearchParams("movie=Heat&run=yes&surprise=1"));
  assert.equal(parsed.run, false);
  assert.ok(parsed.errors.some((error) => error.startsWith("run ")));
  assert.ok(parsed.errors.some((error) => error.includes("surprise")));
});

test("default dates follow the selected timezone, not the machine's", () => {
  const instant = new Date("2026-10-02T05:30:00Z");
  assert.equal(dateInTimeZone("America/New_York", 0, instant), "2026-10-02");
  assert.equal(dateInTimeZone("America/Los_Angeles", 0, instant), "2026-10-01");
  assert.equal(dateInTimeZone("America/Los_Angeles", 1, instant), "2026-10-02");
  assert.equal(dateInTimeZone("Pacific/Auckland", 7, new Date("2026-12-31T20:00:00Z")), "2027-01-08");
});
