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

const localDate = (date: Date) => {
  const year = date.getFullYear();
  const month = `${date.getMonth() + 1}`.padStart(2, "0");
  const day = `${date.getDate()}`.padStart(2, "0");
  return `${year}-${month}-${day}`;
};

export function createDefaultQuery(): QueryState {
  const start = new Date();
  start.setDate(start.getDate() + 1);
  const end = new Date(start);
  end.setDate(end.getDate() + 6);
  return {
    movie: "",
    location: "",
    dateStart: localDate(start),
    dateEnd: localDate(end),
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

export function recommendationLabels(recommendation: Recommendation) {
  return recommendation.seats.map((seat) => seat.label);
}
