export async function GET(request: Request) {
  const apiBase = process.env.CENTERSEAT_API_URL?.replace(/\/$/, "");
  if (!apiBase || !/^https?:\/\//.test(apiBase)) {
    return Response.json({ title: "Live inventory is not connected", detail: "Alternative seat maps require the local query service." }, { status: 503 });
  }
  const parameters = new URL(request.url).searchParams;
  const queryID = parameters.get("query_id")?.trim() ?? "";
  const rank = parameters.get("rank")?.trim() ?? "";
  if (!/^qry_[a-f0-9]+$/.test(queryID) || !/^[1-5]$/.test(rank)) {
    return Response.json({ title: "Invalid recommendation", detail: "A valid query and recommendation rank are required." }, { status: 400 });
  }
  try {
    const upstream = await fetch(`${apiBase}/v1/seat-queries/${queryID}/recommendations/${rank}`, {
      cache: "no-store",
      signal: AbortSignal.timeout(6_000),
    });
    return new Response(await upstream.arrayBuffer(), {
      status: upstream.status,
      headers: {
        "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
        "Cache-Control": "no-store",
        ...(upstream.headers.get("ETag") ? { ETag: upstream.headers.get("ETag") as string } : {}),
      },
    });
  } catch {
    return Response.json({ title: "Seat map unavailable", detail: "The selected live seat map could not be refreshed." }, { status: 503 });
  }
}
