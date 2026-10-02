export type TimeMode = "any" | "inside" | "outside" | "before" | "after";
export type SeatProfile =
  | "balanced"
  | "dead_center"
  | "two_thirds_back"
  | "aisle"
  | "front"
  | "back"
  | "custom";

export type NormalizedSeatZone = {
  minimumX: number;
  maximumX: number;
  minimumY: number;
  maximumY: number;
};

export type QueryState = {
  movie: string;
  movieId?: string;
  location: string;
  /** IANA zone that defines the date range, time window, and displayed times. */
  timezone: string;
  latitude?: number;
  longitude?: number;
  dateStart: string;
  dateEnd: string;
  tickets: number;
  timeMode: TimeMode;
  startTime: string;
  endTime: string;
  profile: SeatProfile;
  customSeatZone: NormalizedSeatZone;
  formats: string[];
  maxDistance: number;
  maxPrice: number;
  limitPrice: boolean;
  recliners: boolean;
  captions: string;
  audioDescription: boolean;
  wheelchairSpaces: number;
  companionSeats: number;
  excludeFirstRows: number;
  allowSplit: boolean;
};

export type Seat = {
  id: string;
  label: string;
  row: string;
  index: number;
  x: number;
  y: number;
  type: "standard" | "recliner" | "wheelchair" | "companion" | "sofa";
  status: "available" | "sold" | "held" | "broken" | "house";
};

export type Showtime = {
  id: string;
  movie_title: string;
  venue_name: string;
  auditorium_name?: string;
  starts_at: string;
  format: string;
  distance_miles: number;
  total_price?: number;
  currency?: string;
  amenities?: string[];
  captions?: string;
  audio_description?: boolean;
  booking_url?: string;
  venue_timezone?: string;
};

export type Recommendation = {
  rank: number;
  showtime: Showtime;
  seats: Seat[];
  seat_options?: Seat[];
  score: number;
  confidence: string;
  explanation: string[];
  score_breakdown: Record<string, number>;
  profile_match: "preferred_zone" | "closest_fallback";
  verified_at: string;
  booking_url?: string;
  seat_map?: {
    seats: Seat[];
    target: { x: number; y: number };
    preferred_depth?: { minimum: number; maximum: number };
    preferred_zone_bounds?: { minimum_x: number; maximum_x: number; minimum_y: number; maximum_y: number };
    recommended_zone?: string[];
    confidence: string;
    observed_at: string;
  };
};

export type SeatQueryResponse = {
  query_id: string;
  status: "complete" | "partial" | "no_match";
  generated_at: string;
  expires_at: string;
  /** Ranks can be refreshed until this instant; afterwards the search must be re-run. */
  refresh_until: string;
  coverage: {
    dates_requested: number;
    dates_with_screenings: number;
    dates_compared: number;
    range_best_proven: boolean;
    screenings_discovered: number;
    screenings_pruned: number;
    inventories_checked: number;
    inventories_fresh: number;
    inventories_failed: number;
    screenings_unavailable: number;
    screenings_price_rejected: number;
    inventory_failure_reasons: Record<string, number>;
    winner_verified: boolean;
    providers_degraded: number;
    discovery_ms: number;
    inventory_ms: number;
    verification_ms: number;
    elapsed_ms: number;
  };
  winner: Recommendation | null;
  alternatives: Recommendation[];
  warnings?: string[];
};

export type ShowtimeQueryResponse = {
  query_id: string;
  status: "complete" | "no_match";
  generated_at: string;
  expires_at: string;
  coverage: SeatQueryResponse["coverage"];
  showtimes: Showtime[];
  warnings?: string[];
};

export type ProviderStatus = {
  name: string;
  kind: "discovery" | "inventory" | "booking_link";
  status: "healthy" | "degraded" | "disabled";
  configured: boolean;
  message?: string;
  coverage?: string;
  location_mode?: "coordinates" | "postal_or_coordinates";
  last_success_at?: string;
};

export type MovieSuggestion = {
  id: string;
  title: string;
  release_date?: string;
  year?: string;
};

export type TrendingMovie = {
  id: string;
  title: string;
  poster_url: string;
  rt_url: string;
  release_text: string;
  critics_score?: number;
  audience_score?: number;
  certified_fresh?: boolean;
};

export const defaultTimeZone = "America/New_York";

export function isValidTimeZone(value: string | undefined | null): value is string {
  if (!value || !/^[A-Za-z_]+(?:\/[A-Za-z0-9_+-]+)*$/.test(value)) return false;
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: value });
    return true;
  } catch {
    return false;
  }
}

export function browserTimeZone(): string {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return isValidTimeZone(zone) ? zone : defaultTimeZone;
}

/** Calendar date (YYYY-MM-DD) in `timeZone`, offset by whole days. */
export function dateInTimeZone(timeZone: string, offsetDays = 0, now = new Date()): string {
  const parts = new Intl.DateTimeFormat("en-CA", { timeZone, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(now);
  const part = (type: string) => Number(parts.find((item) => item.type === type)?.value);
  const shifted = new Date(Date.UTC(part("year"), part("month") - 1, part("day") + offsetDays));
  return shifted.toISOString().slice(0, 10);
}

export function createDefaultQuery(timeZone: string = defaultTimeZone): QueryState {
  return {
    movie: "",
    location: "",
    timezone: timeZone,
    dateStart: dateInTimeZone(timeZone, 1),
    dateEnd: dateInTimeZone(timeZone, 7),
    tickets: 2,
    timeMode: "any",
    startTime: "17:00",
    endTime: "22:00",
    profile: "dead_center",
    customSeatZone: { minimumX: .28, maximumX: .72, minimumY: .42, maximumY: .76 },
    formats: ["Standard", "Dolby", "IMAX"],
    maxDistance: 25,
    maxPrice: 60,
    limitPrice: false,
    recliners: false,
    captions: "any",
    audioDescription: false,
    wheelchairSpaces: 0,
    companionSeats: 0,
    excludeFirstRows: 1,
    allowSplit: false,
  };
}

/** Backend `QueryRequest` JSON (see openapi/v1.yaml SeatQueryRequest). */
export type QueryRequestPayload = {
  movie_query: string;
  movie_id?: string;
  location: { query: string; latitude?: number; longitude?: number; radius_miles: number };
  dates: { start: string; end: string };
  time: { mode: TimeMode; start: string; end: string; timezone: string };
  ticket_count: number;
  seat_profile: SeatProfile;
  custom_seat_zone?: { minimum_x: number; maximum_x: number; minimum_y: number; maximum_y: number };
  formats: string[];
  captions: string;
  audio_description: boolean;
  wheelchair_spaces: number;
  companion_seats: number;
  amenities_required: string[];
  max_distance_miles: number;
  max_total_price?: number;
  allow_unknown_price: boolean;
  exclude_first_rows: number;
  allow_split_party: boolean;
  minimum_geometry_confidence: string;
};

/** Backend request for a search state. The timezone is always explicit. */
export function queryStateToRequest(state: QueryState): QueryRequestPayload {
  return {
    movie_query: state.movie.trim(),
    ...(state.movieId ? { movie_id: state.movieId } : {}),
    location: {
      query: state.location.trim(),
      ...(state.latitude !== undefined ? { latitude: state.latitude } : {}),
      ...(state.longitude !== undefined ? { longitude: state.longitude } : {}),
      radius_miles: state.maxDistance,
    },
    dates: { start: state.dateStart, end: state.dateEnd },
    time: { mode: state.timeMode, start: state.startTime, end: state.endTime, timezone: state.timezone },
    ticket_count: state.tickets,
    seat_profile: state.profile,
    ...(state.profile === "custom" ? { custom_seat_zone: {
      minimum_x: state.customSeatZone.minimumX,
      maximum_x: state.customSeatZone.maximumX,
      minimum_y: state.customSeatZone.minimumY,
      maximum_y: state.customSeatZone.maximumY,
    } } : {}),
    formats: state.formats.map((format) => format.toLowerCase()),
    captions: state.captions,
    audio_description: state.audioDescription,
    wheelchair_spaces: state.wheelchairSpaces,
    companion_seats: state.companionSeats,
    amenities_required: state.recliners ? ["recliner"] : [],
    max_distance_miles: state.maxDistance,
    ...(state.limitPrice ? { max_total_price: state.maxPrice } : {}),
    allow_unknown_price: !state.limitPrice,
    exclude_first_rows: state.excludeFirstRows,
    allow_split_party: state.allowSplit,
    minimum_geometry_confidence: "row_geometry",
  };
}

/** Date/time labels for a screening in an explicit zone (never the viewer's ambient zone). */
export function formatShowtime(startsAt: string, timeZone: string) {
  const instant = new Date(startsAt);
  return {
    date: new Intl.DateTimeFormat("en-US", { timeZone, weekday: "short", month: "short", day: "numeric" }).format(instant),
    time: new Intl.DateTimeFormat("en-US", { timeZone, hour: "numeric", minute: "2-digit" }).format(instant),
    zone: new Intl.DateTimeFormat("en-US", { timeZone, timeZoneName: "short" }).formatToParts(instant).find((part) => part.type === "timeZoneName")?.value ?? timeZone,
  };
}

export type CheckoutHandoff = {
  movie: string;
  venue: string;
  auditorium?: string;
  starts_at: string;
  local_start: string;
  timezone: string;
  format: string;
  ticket_count: number;
  seats: string[];
  booking_url?: string;
  verified_at: string;
  share_url?: string;
  instructions: string;
};

/**
 * Exact-seat purchase handoff for a person or a browser agent. CenterSeat never
 * holds seats; the provider checkout must select these seats and stop before payment.
 */
export function buildCheckoutHandoff(recommendation: Recommendation, ticketCount: number, timeZone: string, shareURL?: string): CheckoutHandoff {
  const labels = formatShowtime(recommendation.showtime.starts_at, timeZone);
  const seats = recommendation.seats.map((seat) => seat.label);
  const bookingURL = recommendation.booking_url || recommendation.showtime.booking_url || undefined;
  return {
    movie: recommendation.showtime.movie_title,
    venue: recommendation.showtime.venue_name,
    ...(recommendation.showtime.auditorium_name ? { auditorium: recommendation.showtime.auditorium_name } : {}),
    starts_at: recommendation.showtime.starts_at,
    local_start: `${labels.date} ${labels.time} ${labels.zone}`,
    timezone: timeZone,
    format: recommendation.showtime.format,
    ticket_count: ticketCount,
    seats,
    ...(bookingURL ? { booking_url: bookingURL } : {}),
    verified_at: recommendation.verified_at,
    ...(shareURL ? { share_url: shareURL } : {}),
    instructions: `Open the provider checkout for ${recommendation.showtime.venue_name} at ${labels.date} ${labels.time} ${labels.zone}, choose ${ticketCount} ticket${ticketCount === 1 ? "" : "s"}, select seats ${seats.join(", ")}, and stop before any payment is submitted. If any seat is no longer available, do not substitute; re-run the CenterSeat search.`,
  };
}

export function handoffText(handoff: CheckoutHandoff): string {
  return [
    `${handoff.movie} — ${handoff.venue}${handoff.auditorium ? ` (${handoff.auditorium})` : ""}`,
    `${handoff.local_start} · ${handoff.format}`,
    `${handoff.ticket_count} ticket${handoff.ticket_count === 1 ? "" : "s"} · seats ${handoff.seats.join(", ")}`,
    handoff.booking_url ? `Checkout: ${handoff.booking_url}` : "No provider checkout link returned",
    handoff.share_url ? `Search: ${handoff.share_url}` : "",
    `Seats verified ${handoff.verified_at}`,
  ].filter(Boolean).join("\n");
}
