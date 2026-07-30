import type { ProviderStatus, Showtime, ShowtimeQueryResponse } from "./api";

type OpenCinemaScreening = {
  id: string;
  film_title?: string;
  theater_id: string;
  theater_name?: string;
  theater_timezone?: string;
  start_time: string;
  formats?: string[];
  is_sold_out?: boolean;
  distance_km?: number | null;
  accessibility_features?: string[];
  checkout?: { type?: string; url?: string | null };
};

type OpenCinemaPage = {
  screenings?: OpenCinemaScreening[];
  pagination?: { has_more?: boolean; next_cursor?: string | null };
  error?: { message?: string };
};

type ShowtimeQuery = {
  movie_query: string;
  location: { latitude: number; longitude: number; radius_miles: number };
  dates: { start: string; end: string };
  time?: { mode?: string; start?: string; end?: string; timezone?: string };
  formats?: string[];
  captions?: string;
  audio_description?: boolean;
  amenities_required?: string[];
  max_distance_miles?: number;
  candidate_limit?: number;
};

const configuration = () => {
  const apiKey = process.env.OPEN_CINEMA_API_KEY?.trim();
  const baseURL = (process.env.OPEN_CINEMA_API_BASE_URL?.trim() || "https://opencinema.app").replace(/\/$/, "");
  if (!apiKey || !/^https:\/\//i.test(baseURL)) return null;
  return { apiKey, baseURL };
};

const apiFetch = async (path: string, signal?: AbortSignal) => {
  const config = configuration();
  if (!config) throw new Error("Open Cinema is not configured");
  return fetch(`${config.baseURL}${path}`, {
    headers: { Accept: "application/json", Authorization: `Bearer ${config.apiKey}` },
    cache: "no-store",
    signal,
  });
};

export async function openCinemaProviderStatuses(): Promise<ProviderStatus[]> {
  if (!configuration()) return [];
  try {
    const response = await apiFetch("/api/v1/public/status", AbortSignal.timeout(5_000));
    if (!response.ok) throw new Error("Open Cinema health check failed");
    return [
      {
        name: "open-cinema-project",
        kind: "discovery",
        status: "healthy",
        configured: true,
        message: "Self-service showtime API connected",
        coverage: "Independent, repertory, and arthouse cinemas",
        last_success_at: new Date().toISOString(),
      },
      {
        name: "seat-inventory-not-connected",
        kind: "inventory",
        status: "disabled",
        configured: false,
        message: "Open Cinema does not provide live per-seat inventory",
      },
    ];
  } catch {
    return [
      {
        name: "open-cinema-project",
        kind: "discovery",
        status: "degraded",
        configured: true,
        message: "Open Cinema health check failed",
      },
      {
        name: "seat-inventory-not-connected",
        kind: "inventory",
        status: "disabled",
        configured: false,
        message: "Open Cinema does not provide live per-seat inventory",
      },
    ];
  }
}

export async function queryOpenCinema(query: ShowtimeQuery): Promise<ShowtimeQueryResponse> {
  if (!configuration()) throw new Error("Open Cinema is not configured");
  if (!query.movie_query?.trim()) throw new Error("movie_query is required");
  if (!Number.isFinite(query.location?.latitude) || !Number.isFinite(query.location?.longitude)) {
    throw new Error("precise latitude and longitude are required");
  }

  const parameters = new URLSearchParams({
    lat: String(query.location.latitude),
    lon: String(query.location.longitude),
    radius_km: String(query.location.radius_miles * 1.609344),
    title: query.movie_query.trim(),
    limit: "200",
  });
  const discovered: OpenCinemaScreening[] = [];
  for (let page = 0; page < 20; page += 1) {
    const response = await apiFetch(`/api/v1/public/screenings?${parameters}`, AbortSignal.timeout(8_000));
    const payload = (await response.json()) as OpenCinemaPage;
    if (!response.ok) throw new Error(payload.error?.message || `Open Cinema returned HTTP ${response.status}`);
    discovered.push(...(payload.screenings ?? []));
    if (!payload.pagination?.has_more) break;
    if (!payload.pagination.next_cursor || page === 19) throw new Error("Open Cinema pagination did not complete safely");
    parameters.set("cursor", payload.pagination.next_cursor);
  }

  const limit = Math.max(1, Math.min(query.candidate_limit ?? 12, 25));
  const showtimes = discovered
    .filter((screening) => matchesScreening(screening, query))
    .map(normalizeScreening)
    .sort((left, right) => left.starts_at.localeCompare(right.starts_at))
    .slice(0, limit);
  const now = new Date();
  return {
    query_id: `stq_${crypto.randomUUID().replaceAll("-", "")}`,
    status: showtimes.length ? "complete" : "no_match",
    generated_at: now.toISOString(),
    expires_at: new Date(now.getTime() + 5 * 60_000).toISOString(),
    coverage: {
      screenings_discovered: discovered.length,
      screenings_pruned: showtimes.length,
      inventories_checked: 0,
      inventories_fresh: 0,
      providers_degraded: 1,
      elapsed_ms: 0,
    },
    showtimes,
    warnings: [
      "Open Cinema supplies real showtimes and provider checkout links, but not live per-seat availability.",
    ],
  };
}

function matchesScreening(screening: OpenCinemaScreening, query: ShowtimeQuery) {
  if (!screening.id || screening.is_sold_out || !screening.start_time) return false;
  const local = localParts(screening.start_time, screening.theater_timezone || query.time?.timezone);
  if (!local || local.date < query.dates.start || local.date > query.dates.end) return false;
  const maxDistance = query.max_distance_miles ?? query.location.radius_miles;
  if (screening.distance_km != null && screening.distance_km / 1.609344 > maxDistance) return false;
  const format = normalizeFormat(screening.formats ?? []);
  if (query.formats?.length && !query.formats.map((value) => value.toLowerCase()).includes(format)) return false;
  const accessibility = (screening.accessibility_features ?? []).join(" ").toLowerCase();
  if (query.audio_description && !/audio[_\s-]desc|descriptive audio/.test(accessibility)) return false;
  if (query.captions === "open" && !/open[_\s-]caption/.test(accessibility)) return false;
  if (query.captions === "closed" && !/closed[_\s-]caption/.test(accessibility)) return false;
  if (query.amenities_required?.length) return false;
  return matchesClock(local.clock, query.time);
}

function normalizeScreening(screening: OpenCinemaScreening): Showtime {
  const accessibility = (screening.accessibility_features ?? []).join(" ").toLowerCase();
  const checkout = validCheckoutURL(screening.checkout?.url);
  return {
    id: screening.id,
    movie_title: screening.film_title || "Untitled screening",
    venue_name: screening.theater_name || screening.theater_id,
    starts_at: new Date(screening.start_time).toISOString(),
    format: normalizeFormat(screening.formats ?? []),
    distance_miles: screening.distance_km == null ? 0 : screening.distance_km / 1.609344,
    amenities: [],
    captions: /open[_\s-]caption/.test(accessibility)
      ? "open"
      : /closed[_\s-]caption/.test(accessibility)
        ? "closed"
        : "none",
    audio_description: /audio[_\s-]desc|descriptive audio/.test(accessibility),
    ...(checkout ? { booking_url: checkout } : {}),
  };
}

function normalizeFormat(formats: string[]) {
  const joined = formats.join(" ").toLowerCase();
  for (const candidate of ["imax", "dolby", "screenx", "rpdx", "xd"] as const) {
    if (joined.includes(candidate)) return candidate;
  }
  if (/3d|real\s*d/.test(joined)) return "3d";
  return "standard";
}

function localParts(value: string, timeZone?: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  try {
    const parts = new Intl.DateTimeFormat("en-US", {
      timeZone: timeZone || "UTC",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    }).formatToParts(date);
    const valueFor = (type: string) => parts.find((part) => part.type === type)?.value || "";
    return {
      date: `${valueFor("year")}-${valueFor("month")}-${valueFor("day")}`,
      clock: `${valueFor("hour")}:${valueFor("minute")}`,
    };
  } catch {
    return null;
  }
}

function matchesClock(clock: string, constraint?: ShowtimeQuery["time"]) {
  const mode = constraint?.mode || "any";
  if (mode === "any") return true;
  if (mode === "inside") return clock >= (constraint?.start || "00:00") && clock <= (constraint?.end || "23:59");
  if (mode === "outside") return clock < (constraint?.start || "00:00") || clock > (constraint?.end || "23:59");
  if (mode === "before") return clock <= (constraint?.end || "23:59");
  if (mode === "after") return clock >= (constraint?.start || "00:00");
  return false;
}

function validCheckoutURL(value?: string | null) {
  if (!value) return "";
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== "https:" || !parsed.hostname || parsed.username || parsed.password) return "";
    if (parsed.hostname === "example.com" || parsed.hostname.endsWith(".example.com") || parsed.hostname === "localhost") return "";
    return parsed.toString();
  } catch {
    return "";
  }
}
