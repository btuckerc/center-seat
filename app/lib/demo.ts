export type TimeMode = "any" | "inside" | "outside" | "before" | "after";
export type SeatProfile =
  | "balanced"
  | "dead_center"
  | "two_thirds_back"
  | "aisle"
  | "front"
  | "back";

export type QueryState = {
  movie: string;
  location: string;
  date: string;
  tickets: number;
  timeMode: TimeMode;
  startTime: string;
  endTime: string;
  profile: SeatProfile;
  formats: string[];
  maxDistance: number;
  maxPrice: number;
  recliners: boolean;
  captions: string;
  audioDescription: boolean;
  wheelchairSpaces: number;
  companionSeats: number;
  excludeFirstRows: number;
  allowSplit: boolean;
};

export type Screening = {
  id: string;
  venue: string;
  room: string;
  time: string;
  format: string;
  distance: number;
  ticketPrice: number;
  seatsAvailable: number;
  row: string;
  startSeat: number;
  score: number;
  amenity: string;
  confidence: string;
};

export const defaultQuery: QueryState = {
  movie: "Spider-Man: Brand New Day",
  location: "Charlotte, NC",
  date: "2026-07-31",
  tickets: 2,
  timeMode: "inside",
  startTime: "17:00",
  endTime: "22:00",
  profile: "balanced",
  formats: ["Dolby", "IMAX", "Standard"],
  maxDistance: 25,
  maxPrice: 60,
  recliners: true,
  captions: "Any captions",
  audioDescription: false,
  wheelchairSpaces: 0,
  companionSeats: 0,
  excludeFirstRows: 1,
  allowSplit: false,
};

const screenings: Screening[] = [
  {
    id: "crown-1820",
    venue: "Crown Arc Cinema",
    room: "Auditorium 6",
    time: "6:20 PM",
    format: "Dolby",
    distance: 4.2,
    ticketPrice: 21.49,
    seatsAvailable: 38,
    row: "F",
    startSeat: 10,
    score: 96,
    amenity: "Dolby Vision · Recliner",
    confidence: "Exact coordinates",
  },
  {
    id: "rialto-2035",
    venue: "Rialto House",
    room: "Screen 2",
    time: "8:35 PM",
    format: "Standard",
    distance: 2.7,
    ticketPrice: 15.25,
    seatsAvailable: 22,
    row: "G",
    startSeat: 8,
    score: 93,
    amenity: "Recliner · Open captions",
    confidence: "Exact coordinates",
  },
  {
    id: "parkside-1910",
    venue: "Parkside 12",
    room: "IMAX 1",
    time: "7:10 PM",
    format: "IMAX",
    distance: 8.1,
    ticketPrice: 24,
    seatsAvailable: 51,
    row: "H",
    startSeat: 12,
    score: 91,
    amenity: "IMAX Laser · Audio description",
    confidence: "Rendered geometry",
  },
  {
    id: "northline-2145",
    venue: "Northline Cinema",
    room: "Dolby 3",
    time: "9:45 PM",
    format: "Dolby",
    distance: 11.6,
    ticketPrice: 19.75,
    seatsAvailable: 17,
    row: "E",
    startSeat: 7,
    score: 88,
    amenity: "Dine-in · Recliner",
    confidence: "Exact coordinates",
  },
  {
    id: "crown-1605",
    venue: "Crown Arc Cinema",
    room: "Auditorium 4",
    time: "4:05 PM",
    format: "Standard",
    distance: 4.2,
    ticketPrice: 14.5,
    seatsAvailable: 63,
    row: "F",
    startSeat: 9,
    score: 90,
    amenity: "Recliner · Matinee",
    confidence: "Row geometry",
  },
  {
    id: "museum-1430",
    venue: "Museum Film Center",
    room: "Hall A",
    time: "2:30 PM",
    format: "Standard",
    distance: 6.8,
    ticketPrice: 12,
    seatsAvailable: 29,
    row: "E",
    startSeat: 6,
    score: 86,
    amenity: "Open captions · Reserved",
    confidence: "Row geometry",
  },
  {
    id: "eastgate-2230",
    venue: "Eastgate Screens",
    room: "XD 1",
    time: "10:30 PM",
    format: "XD",
    distance: 13.4,
    ticketPrice: 18.95,
    seatsAvailable: 42,
    row: "G",
    startSeat: 10,
    score: 84,
    amenity: "Recliner · Closed captions",
    confidence: "Exact coordinates",
  },
];

const toMinutes = (label: string) => {
  if (label.includes("M")) {
    const [time, period] = label.split(" ");
    const [rawHour, minute] = time.split(":").map(Number);
    const hour = (rawHour % 12) + (period === "PM" ? 12 : 0);
    return hour * 60 + minute;
  }
  const [hour, minute] = label.split(":").map(Number);
  return hour * 60 + minute;
};

const matchesTime = (screening: Screening, query: QueryState) => {
  const time = toMinutes(screening.time);
  const start = toMinutes(query.startTime);
  const end = toMinutes(query.endTime);
  switch (query.timeMode) {
    case "inside":
      return time >= start && time <= end;
    case "outside":
      return time < start || time > end;
    case "before":
      return time <= end;
    case "after":
      return time >= start;
    default:
      return true;
  }
};

export function runDemoQuery(query: QueryState) {
  const matches = screenings
    .filter((screening) => query.formats.includes(screening.format))
    .filter((screening) => screening.distance <= query.maxDistance)
    .filter((screening) => screening.ticketPrice * query.tickets <= query.maxPrice)
    .filter((screening) => screening.seatsAvailable >= query.tickets)
    .filter((screening) => matchesTime(screening, query))
    .map((screening) => {
      const profileAdjustment =
        query.profile === "aisle"
          ? -1
          : query.profile === "dead_center"
            ? 1
            : query.profile === "two_thirds_back"
              ? 0.5
              : 0;
      const distanceAdjustment = Math.max(0, screening.distance - 5) * 0.18;
      return {
        ...screening,
        score: Math.round((screening.score + profileAdjustment - distanceAdjustment) * 10) / 10,
      };
    })
    .sort((a, b) => b.score - a.score);

  return {
    winner: matches[0] ?? null,
    alternatives: matches.slice(1, 4),
    coverage: {
      discovered: 34,
      matched: Math.max(matches.length + 3, matches.length),
      checked: Math.min(Math.max(matches.length + 3, 5), 12),
      fresh: matches.length,
      elapsed: 684,
    },
  };
}

export function seatLabels(screening: Screening, count: number) {
  return Array.from(
    { length: count },
    (_, index) => `${screening.row}${screening.startSeat + index}`,
  );
}
