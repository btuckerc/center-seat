const configurationProblem = () =>
  Response.json(
    {
      type: "https://centerseat.invalid/problems/provider-not-configured",
      title: "Live inventory is not connected",
      status: 503,
      detail:
        "The deployed query service has no live showtime and seat-inventory endpoint configured. CenterSeat will not substitute demo results.",
    },
    { status: 503 },
  );

export async function POST(request: Request) {
  const apiBase = process.env.CENTERSEAT_API_URL?.replace(/\/$/, "");
  if (!apiBase || !/^https?:\/\//.test(apiBase)) return configurationProblem();

  const body = await request.text();
  try {
    const upstream = await fetch(`${apiBase}/v1/seat-queries`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key":
          request.headers.get("Idempotency-Key") ?? crypto.randomUUID(),
        "X-Request-ID": request.headers.get("X-Request-ID") ?? crypto.randomUUID(),
      },
      body,
      cache: "no-store",
      signal: AbortSignal.timeout(15_000),
    });
    return new Response(await upstream.arrayBuffer(), {
      status: upstream.status,
      headers: {
        "Content-Type":
          upstream.headers.get("Content-Type") ?? "application/json",
        "Cache-Control": "no-store",
        ...(upstream.headers.get("ETag")
          ? { ETag: upstream.headers.get("ETag") as string }
          : {}),
      },
    });
  } catch {
    return Response.json(
      {
        type: "https://centerseat.invalid/problems/provider-unavailable",
        title: "Live inventory is temporarily unavailable",
        status: 503,
        detail:
          "The configured query service could not be reached. No cached or fabricated result was returned.",
      },
      { status: 503 },
    );
  }
}
