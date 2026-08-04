import type { TrendingMovie } from "./api";

type RottenTomatoesMovie = {
  audienceScore?: { scorePercent?: string };
  criticsScore?: { certified?: boolean; scorePercent?: string };
  emsId?: string;
  mediaUrl?: string;
  posterUri?: string;
  releaseDateText?: string;
  title?: string;
  type?: string;
};

export type TrendingPayload = {
  movies: TrendingMovie[];
  source: "Rotten Tomatoes";
  source_url: string;
  unavailable: boolean;
};

export const trendingSourceURL = "https://www.rottentomatoes.com/browse/movies_in_theaters/sort:popular";
const trendingDataURL = "https://www.rottentomatoes.com/cnapi/browse/movies_in_theaters/sort:popular?page=1";
const freshForMs = 15 * 60 * 1_000;
const staleForMs = 24 * 60 * 60 * 1_000;
const configuredTimeoutMs = Number.parseInt(process.env.CENTERSEAT_TRENDING_TIMEOUT_MS ?? "", 10);
const upstreamTimeoutMs = Number.isFinite(configuredTimeoutMs) && configuredTimeoutMs > 0 ? configuredTimeoutMs : 4_000;

let cachedPayload: TrendingPayload | undefined;
let cachedAt = 0;
let pendingPayload: Promise<TrendingPayload> | undefined;

const score = (value?: string) => {
  const parsed = Number.parseInt(value?.replace("%", "") ?? "", 10);
  return Number.isFinite(parsed) ? parsed : undefined;
};

const unavailablePayload = (): TrendingPayload => ({
  movies: [],
  source: "Rotten Tomatoes",
  source_url: trendingSourceURL,
  unavailable: true,
});

async function loadTrending(): Promise<TrendingPayload> {
  const upstream = await fetch(trendingDataURL, {
    headers: {
      Accept: "application/json",
      "User-Agent": "CenterSeat/0.1 personal read-only movie discovery",
    },
    cache: "force-cache",
    next: { revalidate: freshForMs / 1_000 },
    signal: AbortSignal.timeout(upstreamTimeoutMs),
  });
  if (!upstream.ok) throw new Error(`Rotten Tomatoes returned HTTP ${upstream.status}`);

  const payload = await upstream.json() as { grid?: { list?: RottenTomatoesMovie[] } };
  const movies = (payload.grid?.list ?? [])
    .filter((movie) => movie.type === "Movie" && movie.title && movie.posterUri && movie.mediaUrl?.startsWith("/m/"))
    .slice(0, 12)
    .map((movie): TrendingMovie => ({
      id: movie.emsId ?? movie.mediaUrl as string,
      title: movie.title as string,
      poster_url: movie.posterUri as string,
      rt_url: `https://www.rottentomatoes.com${movie.mediaUrl}`,
      release_text: movie.releaseDateText ?? "In theaters",
      critics_score: score(movie.criticsScore?.scorePercent),
      audience_score: score(movie.audienceScore?.scorePercent),
      certified_fresh: movie.criticsScore?.certified === true,
    }));

  if (!movies.length) throw new Error("Rotten Tomatoes returned no movies");
  return {
    movies,
    source: "Rotten Tomatoes",
    source_url: trendingSourceURL,
    unavailable: false,
  };
}

export async function getTrendingMovies(): Promise<TrendingPayload> {
  const now = Date.now();
  if (cachedPayload && now - cachedAt < freshForMs) return cachedPayload;
  if (pendingPayload) return pendingPayload;

  pendingPayload = loadTrending()
    .then((payload) => {
      cachedPayload = payload;
      cachedAt = Date.now();
      return payload;
    })
    .catch(() => cachedPayload && now - cachedAt < staleForMs ? cachedPayload : unavailablePayload())
    .finally(() => {
      pendingPayload = undefined;
    });

  return pendingPayload;
}
