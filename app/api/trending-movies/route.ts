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

const sourceURL = "https://www.rottentomatoes.com/browse/movies_in_theaters/sort:popular";
const dataURL = "https://www.rottentomatoes.com/cnapi/browse/movies_in_theaters/sort:popular?page=1";

const score = (value?: string) => {
  const parsed = Number.parseInt(value?.replace("%", "") ?? "", 10);
  return Number.isFinite(parsed) ? parsed : undefined;
};

export async function GET() {
  try {
    const upstream = await fetch(dataURL, {
      headers: {
        Accept: "application/json",
        "User-Agent": "CenterSeat/0.1 personal read-only movie discovery",
      },
      cache: "no-store",
      signal: AbortSignal.timeout(5_000),
    });
    if (!upstream.ok) throw new Error(`Rotten Tomatoes returned HTTP ${upstream.status}`);
    const payload = await upstream.json() as { grid?: { list?: RottenTomatoesMovie[] } };
    const movies = (payload.grid?.list ?? [])
      .filter((movie) => movie.type === "Movie" && movie.title && movie.posterUri && movie.mediaUrl?.startsWith("/m/"))
      .slice(0, 12)
      .map((movie) => ({
        id: movie.emsId ?? movie.mediaUrl,
        title: movie.title as string,
        poster_url: movie.posterUri as string,
        rt_url: `https://www.rottentomatoes.com${movie.mediaUrl}`,
        release_text: movie.releaseDateText ?? "In theaters",
        critics_score: score(movie.criticsScore?.scorePercent),
        audience_score: score(movie.audienceScore?.scorePercent),
        certified_fresh: movie.criticsScore?.certified === true,
      }));
    return Response.json(
      { movies, source: "Rotten Tomatoes", source_url: sourceURL, unavailable: movies.length === 0 },
      { headers: { "Cache-Control": "private, max-age=900, stale-while-revalidate=3600" } },
    );
  } catch {
    return Response.json(
      { movies: [], source: "Rotten Tomatoes", source_url: sourceURL, unavailable: true },
      { headers: { "Cache-Control": "private, max-age=60" } },
    );
  }
}
