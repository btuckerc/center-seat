import { queryOpenCinema } from "../../lib/opencinema";

export async function POST(request: Request) {
  const apiBase = process.env.CENTERSEAT_API_URL?.replace(/\/$/, "");
  const body = await request.text();
  if (apiBase && /^https?:\/\//.test(apiBase)) {
    try {
      const upstream = await fetch(`${apiBase}/v1/showtime-queries`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": request.headers.get("Idempotency-Key") ?? crypto.randomUUID(),
          "X-Request-ID": request.headers.get("X-Request-ID") ?? crypto.randomUUID(),
        },
        body,
        cache: "no-store",
        signal: AbortSignal.timeout(15_000),
      });
      return new Response(await upstream.arrayBuffer(), {
        status: upstream.status,
        headers: { "Content-Type": upstream.headers.get("Content-Type") ?? "application/json", "Cache-Control": "no-store" },
      });
    } catch {
      return Response.json({ title: "Showtime service unavailable", detail: "The configured query service could not be reached." }, { status: 503 });
    }
  }
  try {
    const result = await queryOpenCinema(JSON.parse(body));
    return Response.json(result, { headers: { "Cache-Control": "private, max-age=60" } });
  } catch (error) {
    return Response.json(
      { title: "Showtime query failed", detail: error instanceof Error ? error.message : "Open Cinema did not return a usable response." },
      { status: 503 },
    );
  }
}
