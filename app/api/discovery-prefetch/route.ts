// Warms showtime discovery for the search being composed. Best effort: the browser ignores the
// outcome, and the real search reports any provider problem itself.
const prefetchTimeoutMs = 10_000;

export async function POST(request: Request) {
  const apiBase = process.env.CENTERSEAT_API_URL?.replace(/\/$/, "");
  if (!apiBase || !/^https?:\/\//.test(apiBase)) {
    return new Response(null, { status: 204, headers: { "Cache-Control": "no-store" } });
  }

  try {
    const upstream = await fetch(`${apiBase}/v1/discovery-prefetch`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: await request.text(),
      cache: "no-store",
      signal: AbortSignal.timeout(prefetchTimeoutMs),
    });
    if (upstream.status === 204) {
      return new Response(null, { status: 204, headers: { "Cache-Control": "no-store" } });
    }
    return new Response(await upstream.arrayBuffer(), {
      status: upstream.status,
      headers: {
        "Content-Type": upstream.headers.get("Content-Type") ?? "application/json",
        "Cache-Control": "no-store",
      },
    });
  } catch {
    return new Response(null, { status: 204, headers: { "Cache-Control": "no-store" } });
  }
}
