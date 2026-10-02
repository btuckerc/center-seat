import { isValidTimeZone, type QueryState, type SeatProfile, type TimeMode } from "./api.ts";

/**
 * Shareable search links. A link describes a search, never a result: it carries
 * an explicit place and IANA timezone so the opening browser's own location and
 * zone are never substituted.
 *
 * v=1 movie movie_id near lat lon tz from to tickets profile zone time start end
 * formats dist price captions ad wc comp skip recl split run
 * Unknown keys and malformed values are returned as errors for the UI.
 */
export const shareVersion = "1";

const timeModes: TimeMode[] = ["any", "inside", "outside", "before", "after"];
const seatProfiles: SeatProfile[] = ["balanced", "dead_center", "two_thirds_back", "aisle", "front", "back", "custom"];
const captionModes = ["any", "open", "closed", "none"];
const datePattern = /^\d{4}-\d{2}-\d{2}$/;
const clockPattern = /^([01]\d|2[0-3]):[0-5]\d$/;
const shareFields: Record<string, true> = { v: true, movie: true, movie_id: true, near: true, lat: true, lon: true, tz: true, from: true, to: true, tickets: true, profile: true, zone: true, time: true, start: true, end: true, formats: true, dist: true, price: true, captions: true, ad: true, wc: true, comp: true, skip: true, recl: true, split: true, run: true };

export function searchParamsFromQuery(state: QueryState, options: { run?: boolean } = {}): URLSearchParams {
  const params = new URLSearchParams({ v: shareVersion, movie: state.movie.trim() });
  if (state.movieId) params.set("movie_id", state.movieId);
  params.set("near", state.location.trim());
  if (state.latitude !== undefined && state.longitude !== undefined) {
    params.set("lat", state.latitude.toFixed(5));
    params.set("lon", state.longitude.toFixed(5));
  }
  params.set("tz", state.timezone);
  params.set("from", state.dateStart);
  params.set("to", state.dateEnd);
  params.set("tickets", String(state.tickets));
  params.set("profile", state.profile);
  if (state.profile === "custom") {
    const zone = state.customSeatZone;
    params.set("zone", [zone.minimumX, zone.maximumX, zone.minimumY, zone.maximumY].join(","));
  }
  params.set("time", state.timeMode);
  if (state.timeMode === "inside" || state.timeMode === "outside" || state.timeMode === "after") params.set("start", state.startTime);
  if (state.timeMode === "inside" || state.timeMode === "outside" || state.timeMode === "before") params.set("end", state.endTime);
  params.set("formats", state.formats.join(","));
  params.set("dist", String(state.maxDistance));
  if (state.limitPrice) params.set("price", String(state.maxPrice));
  if (state.captions !== "any") params.set("captions", state.captions);
  if (state.audioDescription) params.set("ad", "1");
  if (state.wheelchairSpaces) params.set("wc", String(state.wheelchairSpaces));
  if (state.companionSeats) params.set("comp", String(state.companionSeats));
  params.set("skip", String(state.excludeFirstRows));
  if (state.recliners) params.set("recl", "1");
  if (state.allowSplit) params.set("split", "1");
  if (options.run) params.set("run", "1");
  return params;
}

export function shareURL(origin: string, state: QueryState, options: { run?: boolean } = {}): string {
  const url = new URL("/", origin);
  url.search = searchParamsFromQuery(state, options).toString();
  return url.toString();
}

export type ParsedShare = {
  /** Fields present and valid in the link; apply over saved preferences and defaults. */
  query: Partial<QueryState>;
  /** Present but malformed fields, reported rather than silently dropped. */
  errors: string[];
  run: boolean;
};

/** Returns null when the URL carries no shared search. */
export function queryFromSearchParams(params: URLSearchParams): ParsedShare | null {
  if (!params.has("v") && !params.has("movie") && ![...params.keys()].some((name) => shareFields[name])) return null;
  const query: Partial<QueryState> = {};
  const errors: string[] = [];
  const version = params.get("v");
  if (version !== null && version !== shareVersion) errors.push(`Unsupported link version ${version}`);
  for (const name of params.keys()) {
    if (!shareFields[name]) errors.push(`Unknown shared-link parameter ${name}`);
  }

  const integer = (name: string, minimum: number, maximum: number, apply: (value: number) => void) => {
    const raw = params.get(name);
    if (raw === null) return;
    const value = Number(raw);
    if (Number.isInteger(value) && value >= minimum && value <= maximum) apply(value);
    else errors.push(`${name} must be a whole number from ${minimum} to ${maximum}`);
  };
  const flag = (name: string, apply: (value: boolean) => void) => {
    const raw = params.get(name);
    if (raw === null) return;
    if (raw === "1" || raw === "0") apply(raw === "1");
    else errors.push(`${name} must be 1 or 0`);
  };

  const movieIDRaw = params.get("movie_id");
  if (movieIDRaw !== null) {
    if (movieIDRaw.trim()) query.movieId = movieIDRaw.trim().slice(0, 128);
    else errors.push("movie_id must not be empty");
  }
  const movie = params.get("movie")?.trim();
  if (movie) query.movie = movie.slice(0, 160);
  const near = params.get("near");
  if (near !== null) query.location = near.trim().slice(0, 160);

  const lat = params.get("lat");
  const lon = params.get("lon");
  if (lat !== null || lon !== null) {
    const latitude = Number(lat);
    const longitude = Number(lon);
    if (lat !== null && lon !== null && Number.isFinite(latitude) && Number.isFinite(longitude) && Math.abs(latitude) <= 90 && Math.abs(longitude) <= 180) {
      query.latitude = latitude;
      query.longitude = longitude;
      if (!query.location) query.location = `${latitude.toFixed(3)}, ${longitude.toFixed(3)}`;
    } else errors.push("lat and lon must be supplied together as valid coordinates");
  }

  const tz = params.get("tz");
  if (tz !== null) {
    if (isValidTimeZone(tz)) query.timezone = tz;
    else errors.push(`tz must be an IANA timezone such as America/New_York`);
  }

  for (const [name, key] of [["from", "dateStart"], ["to", "dateEnd"]] as const) {
    const raw = params.get(name);
    if (raw === null) continue;
    if (datePattern.test(raw) && !Number.isNaN(Date.parse(`${raw}T00:00:00Z`))) query[key] = raw;
    else errors.push(`${name} must be YYYY-MM-DD`);
  }
  if (query.dateStart && query.dateEnd && query.dateEnd < query.dateStart) errors.push("to must not be before from");

  integer("tickets", 1, 8, (value) => { query.tickets = value; });

  const profile = params.get("profile");
  if (profile !== null) {
    if ((seatProfiles as string[]).includes(profile)) query.profile = profile as SeatProfile;
    else errors.push(`profile must be one of ${seatProfiles.join(", ")}`);
  }
  const zone = params.get("zone");
  if (zone !== null) {
    const [minimumX, maximumX, minimumY, maximumY] = zone.split(",").map(Number);
    const values = [minimumX, maximumX, minimumY, maximumY];
    if (values.every((value) => Number.isFinite(value) && value >= 0 && value <= 1) && minimumX < maximumX && minimumY < maximumY) {
      query.customSeatZone = { minimumX, maximumX, minimumY, maximumY };
    } else errors.push("zone must be minX,maxX,minY,maxY within 0–1");
  }

  const time = params.get("time");
  if (time !== null) {
    if ((timeModes as string[]).includes(time)) query.timeMode = time as TimeMode;
    else errors.push(`time must be one of ${timeModes.join(", ")}`);
  }
  for (const [name, key] of [["start", "startTime"], ["end", "endTime"]] as const) {
    const raw = params.get(name);
    if (raw === null) continue;
    if (clockPattern.test(raw)) query[key] = raw;
    else errors.push(`${name} must be HH:MM (24-hour)`);
  }

  const formats = params.get("formats");
  if (formats !== null) {
    const list = formats.split(",").map((item) => item.trim()).filter(Boolean);
    if (list.length) query.formats = [...new Set(list)];
    else errors.push("formats must list at least one format");
  }
  integer("dist", 2, 49, (value) => { query.maxDistance = value; });
  const price = params.get("price");
  if (price !== null) {
    const value = Number(price);
    if (Number.isFinite(value) && value >= 10 && value <= 200) {
      query.limitPrice = true;
      query.maxPrice = value;
    } else errors.push("price must be a total from 10 to 200");
  }
  const captions = params.get("captions");
  if (captions !== null) {
    if (captionModes.includes(captions)) query.captions = captions;
    else errors.push(`captions must be one of ${captionModes.join(", ")}`);
  }
  flag("ad", (value) => { query.audioDescription = value; });
  integer("wc", 0, 4, (value) => { query.wheelchairSpaces = value; });
  integer("comp", 0, 4, (value) => { query.companionSeats = value; });
  integer("skip", 0, 4, (value) => { query.excludeFirstRows = value; });
  flag("recl", (value) => { query.recliners = value; });
  flag("split", (value) => { query.allowSplit = value; });

  const run = params.get("run");
  if (run !== null && run !== "1" && run !== "0") errors.push("run must be 1 or 0");
  return { query, errors, run: run === "1" };
}
