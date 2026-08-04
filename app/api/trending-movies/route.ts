import { getTrendingMovies } from "../../lib/trending.server";

export async function GET() {
  const payload = await getTrendingMovies();
  const cacheControl = payload.unavailable
    ? "public, max-age=30, stale-if-error=300"
    : "public, max-age=900, stale-while-revalidate=21600, stale-if-error=86400";
  return Response.json(payload, { headers: { "Cache-Control": cacheControl } });
}
