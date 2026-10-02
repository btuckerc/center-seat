import { buildCheckoutHandoff, createDefaultQuery, formatShowtime, isValidTimeZone, queryStateToRequest, type QueryState, type Recommendation, type SeatQueryResponse, type Showtime, type ShowtimeQueryResponse, type TimeMode, type SeatProfile } from "./api.ts";
import { shareURL } from "./share.ts";

export class AgentError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) { super(message); this.status = status; this.code = code; }
}

export function authorize(request: Request): Response | null {
  const expected = process.env.CENTERSEAT_AGENT_TOKEN;
  if (!expected) return new Response(null, { status: 404, headers: { "Cache-Control": "no-store" } });
  const supplied = request.headers.get("Authorization")?.match(/^Bearer (.+)$/i)?.[1] ?? "";
  const a = Buffer.from(supplied), b = Buffer.from(expected);
  let difference = a.length ^ b.length;
  for (let i = 0; i < Math.max(a.length, b.length); i++) difference |= (a[i] ?? 0) ^ (b[i] ?? 0);
  if (!supplied || difference !== 0) return new Response(JSON.stringify({ type: "about:blank", title: "Unauthorized", status: 401, code: "unauthorized", detail: "A valid bearer token is required." }), { status: 401, headers: { "Content-Type": "application/problem+json", "WWW-Authenticate": "Bearer", "Cache-Control": "no-store" } });
  return null;
}

function error(status: number, code: string, message: string): never { throw new AgentError(status, code, message); }
function record(value: unknown, name: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) error(400, "invalid_request", `${name} must be an object`);
  return value as Record<string, unknown>;
}
function numberField(value: unknown, name: string, min: number, max: number, integer = false): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value < min || value > max || (integer && !Number.isInteger(value))) error(400, "invalid_request", `${name} must be ${integer ? "an integer" : "a number"} from ${min} to ${max}`);
  return value;
}
function requiredText(value: unknown, name: string, max = 160): string {
  if (typeof value !== "string" || !value.trim() || value.trim().length > max) error(400, "invalid_request", `${name} is required and must be at most ${max} characters`);
  return value.trim();
}
const profiles: SeatProfile[] = ["balanced", "dead_center", "two_thirds_back", "aisle", "front", "back", "custom"];
const modes: TimeMode[] = ["any", "inside", "outside", "before", "after"];
function isProfile(value: unknown): value is SeatProfile {
  return typeof value === "string" && (profiles as string[]).includes(value);
}
function isMode(value: unknown): value is TimeMode {
  return typeof value === "string" && (modes as string[]).includes(value);
}
const clock = /^(?:[01]\d|2[0-3]):[0-5]\d$/;

function validateSearch(input: unknown): QueryState {
  const body = record(input, "request");
  const movie = requiredText(body.movie, "movie");
  const timezone = requiredText(body.timezone, "timezone");
  if (!isValidTimeZone(timezone)) error(400, "invalid_request", "timezone must be a valid IANA timezone");
  const location = record(body.location, "location");
  const zip = typeof location.zip === "string" ? location.zip.trim() : "";
  const query = typeof location.query === "string" ? location.query.trim() : "";
  const hasLat = location.latitude !== undefined, hasLon = location.longitude !== undefined;
  if (hasLat !== hasLon) error(400, "invalid_request", "location.latitude and location.longitude must be supplied together");
  if (!zip && !query && !hasLat) error(400, "invalid_request", "location requires zip or query, or both latitude and longitude");
  const latitude = hasLat ? numberField(location.latitude, "location.latitude", -90, 90) : undefined;
  const longitude = hasLon ? numberField(location.longitude, "location.longitude", -180, 180) : undefined;
  const state = createDefaultQuery(timezone);
  state.movie = movie;
  if (body.movie_id !== undefined) state.movieId = requiredText(body.movie_id, "movie_id", 128);
  state.location = zip || query || `${latitude}, ${longitude}`;
  state.latitude = latitude; state.longitude = longitude;
  if (body.dates !== undefined) {
    const dates = record(body.dates, "dates");
    if (dates.start !== undefined) state.dateStart = requiredText(dates.start, "dates.start", 10);
    if (dates.end !== undefined) state.dateEnd = requiredText(dates.end, "dates.end", 10);
  }
  const dateValid = (d: string) => /^\d{4}-\d{2}-\d{2}$/.test(d) && !Number.isNaN(Date.parse(`${d}T00:00:00Z`)) && new Date(`${d}T00:00:00Z`).toISOString().slice(0, 10) === d;
  if (!dateValid(state.dateStart) || !dateValid(state.dateEnd) || state.dateEnd < state.dateStart) error(400, "invalid_request", "dates.start and dates.end must be valid YYYY-MM-DD dates with end not before start");
  if (body.tickets !== undefined) state.tickets = numberField(body.tickets, "tickets", 1, 8, true);
  if (body.profile !== undefined) {
    if (!isProfile(body.profile)) error(400, "invalid_request", `profile must be one of ${profiles.join(", ")}`);
    state.profile = body.profile;
  }
  if (body.time !== undefined) {
    const time = record(body.time, "time");
    if (time.mode !== undefined) { if (!isMode(time.mode)) error(400, "invalid_request", `time.mode must be one of ${modes.join(", ")}`); state.timeMode = time.mode; }
    for (const key of ["start", "end"] as const) if (time[key] !== undefined) { if (typeof time[key] !== "string" || !clock.test(time[key])) error(400, "invalid_request", `time.${key} must be HH:MM (24-hour)`); if (key === "start") state.startTime = time[key]; else state.endTime = time[key]; }
  }
  if (body.formats !== undefined) { if (!Array.isArray(body.formats) || !body.formats.length || body.formats.some((f: unknown) => typeof f !== "string" || !f.trim())) error(400, "invalid_request", "formats must be a non-empty array of strings"); state.formats = body.formats.map((f: string) => f.trim()); }
  if (body.max_distance_miles !== undefined) state.maxDistance = numberField(body.max_distance_miles, "max_distance_miles", 2, 49);
  if (body.max_total_price !== undefined) { state.maxPrice = numberField(body.max_total_price, "max_total_price", 0, 10000); state.limitPrice = true; }
  if (body.captions !== undefined) { if (typeof body.captions !== "string" || !["any", "open", "closed", "none"].includes(body.captions)) error(400, "invalid_request", "captions must be any, open, closed, or none"); state.captions = body.captions; }
  if (body.accessibility !== undefined) {
    const a = record(body.accessibility, "accessibility");
    if (a.wheelchair_spaces !== undefined) state.wheelchairSpaces = numberField(a.wheelchair_spaces, "accessibility.wheelchair_spaces", 0, 8, true);
    if (a.companion_seats !== undefined) state.companionSeats = numberField(a.companion_seats, "accessibility.companion_seats", 0, 8, true);
    if (a.audio_description !== undefined) { if (typeof a.audio_description !== "boolean") error(400, "invalid_request", "accessibility.audio_description must be boolean"); state.audioDescription = a.audio_description; }
  }
  if (body.exclude_first_rows !== undefined) state.excludeFirstRows = numberField(body.exclude_first_rows, "exclude_first_rows", 0, 20, true);
  if (body.recliners !== undefined) { if (typeof body.recliners !== "boolean") error(400, "invalid_request", "recliners must be boolean"); state.recliners = body.recliners; }
  if (body.allow_split_party !== undefined) { if (typeof body.allow_split_party !== "boolean") error(400, "invalid_request", "allow_split_party must be boolean"); state.allowSplit = body.allow_split_party; }
  return state;
}
function baseURL() { const value = process.env.CENTERSEAT_API_URL?.replace(/\/$/, ""); if (!value || !/^https?:\/\//.test(value)) error(503, "provider_unavailable", "The CenterSeat API is not configured"); return value; }
async function backend<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try { response = await fetch(`${baseURL()}${path}`, { ...init, cache: "no-store", signal: AbortSignal.timeout(45_000) }); }
  catch { error(503, "provider_unavailable", "The CenterSeat API could not be reached"); }
  const text = await response.text(); let data: Record<string, unknown> = {};
  try {
    const parsed: unknown = text ? JSON.parse(text) : {};
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) data = parsed as Record<string, unknown>;
    else data = { detail: text };
  } catch { data = { detail: text }; }
  if (!response.ok) throw new AgentError(response.status, typeof data.code === "string" ? data.code : "upstream_error", typeof data.detail === "string" ? data.detail : typeof data.title === "string" ? data.title : `CenterSeat API returned ${response.status}`);
  return data as T;
}
function originFor(origin?: string) { return process.env.CENTERSEAT_PUBLIC_URL || origin || "https://movies.angl.gg"; }
function summarize(rec: Recommendation, tz: string) {
  const local = formatShowtime(rec.showtime.starts_at, tz);
  return { rank: rec.rank, venue: rec.showtime.venue_name, auditorium: rec.showtime.auditorium_name, local_start: `${local.date} ${local.time} ${local.zone}`, format: rec.showtime.format, seats: rec.seats.map(s => s.label), score: rec.score, profile_match: rec.profile_match, verified_at: rec.verified_at, booking_url: rec.booking_url || rec.showtime.booking_url };
}
export async function searchSeats(input: unknown, origin?: string) {
  const state = validateSearch(input);
  const providers = await backend<{ providers?: Array<{kind?: string; configured?: boolean; status?: string}> }>("/v1/providers");
  const live = providers.providers?.some(p => p.kind === "inventory" && p.configured === true && p.status !== "disabled") ?? false;
  const path = live ? "/v1/seat-queries" : "/v1/showtime-queries";
  const data = await backend<SeatQueryResponse | ShowtimeQueryResponse>(path, { method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() }, body: JSON.stringify(queryStateToRequest(state)) });
  const share_url = shareURL(originFor(origin), state);
  const common = { status: data.status, query_id: data.query_id, expires_at: data.expires_at, share_url, coverage: { range_best_proven: data.coverage.range_best_proven, winner_verified: data.coverage.winner_verified, inventories_fresh: data.coverage.inventories_fresh, inventories_failed: data.coverage.inventories_failed, dates_compared: data.coverage.dates_compared, dates_with_screenings: data.coverage.dates_with_screenings }, warnings: data.warnings ?? [] };
  if (live) {
    const seatData = data as SeatQueryResponse;
    return { ...common, refresh_until: seatData.refresh_until, winner: seatData.winner ? { ...summarize(seatData.winner, state.timezone), handoff: buildCheckoutHandoff(seatData.winner, seatData.winner.seats.length, state.timezone, share_url) } : null, alternatives: seatData.alternatives.map(r => summarize(r, state.timezone)) };
  }
  return { ...common, showtimes: (data as ShowtimeQueryResponse).showtimes.map((s: Showtime) => { const local = formatShowtime(s.starts_at, state.timezone); return { venue: s.venue_name, auditorium: s.auditorium_name, local_start: `${local.date} ${local.time} ${local.zone}`, format: s.format, booking_url: s.booking_url }; }) };
}
export async function refreshRecommendation(input: {query_id: string; rank: number; timezone: string}) {
  if (!input || typeof input.query_id !== "string" || !input.query_id.trim()) error(400, "invalid_request", "query_id is required");
  if (!Number.isInteger(input.rank) || input.rank < 1) error(400, "invalid_request", "rank must be a positive integer");
  if (!isValidTimeZone(input.timezone)) error(400, "invalid_request", "timezone must be a valid IANA timezone");
  const rec = await backend<Recommendation>(`/v1/seat-queries/${encodeURIComponent(input.query_id)}/recommendations/${input.rank}`);
  const handoff = buildCheckoutHandoff(rec, rec.seats.length, input.timezone, undefined);
  return { ...summarize(rec, input.timezone), handoff };
}
export async function suggestMovies(q: string) {
  if (typeof q !== "string" || !q.trim()) error(400, "invalid_request", "q is required");
  return backend("/v1/movie-suggestions?q=" + encodeURIComponent(q.trim()));
}
export async function listProviders() { return backend("/v1/providers"); }
