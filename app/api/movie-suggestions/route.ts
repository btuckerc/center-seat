export async function GET(request: Request) {
  const apiBase = process.env.CENTERSEAT_API_URL?.replace(/\/$/, "");
  const query = new URL(request.url).searchParams.get("q")?.trim() ?? "";
  if (query.length < 2) return Response.json({ suggestions: [] });
  if (!apiBase || !/^https?:\/\//.test(apiBase)) {
    return Response.json({ suggestions: [] }, { headers: { "Cache-Control": "private, max-age=60" } });
  }
  try {
    const upstream = await fetch(`${apiBase}/v1/movie-suggestions?q=${encodeURIComponent(query)}&limit=6`, {
      headers: request.headers.get("If-None-Match") ? { "If-None-Match": request.headers.get("If-None-Match") as string } : {},
      cache: "no-store",
      signal: AbortSignal.timeout(5_000),
    });
    return new Response(await upstream.arrayBuffer(), {
      status: upstream.status,
      headers: {
        "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
        "Cache-Control": "private, max-age=60",
        ...(upstream.headers.get("ETag") ? { ETag: upstream.headers.get("ETag") as string } : {}),
      },
    });
  } catch {
    return Response.json({ suggestions: [] }, { headers: { "Cache-Control": "no-store" } });
  }
}
