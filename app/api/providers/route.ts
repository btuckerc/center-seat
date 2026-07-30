import { openCinemaProviderStatuses } from "../../lib/opencinema";

export async function GET() {
  const apiBase = process.env.CENTERSEAT_API_URL?.replace(/\/$/, "");
  if (!apiBase || !/^https?:\/\//.test(apiBase)) {
    const providers = await openCinemaProviderStatuses();
    if (providers.length) return Response.json({ providers, configured: true }, { headers: { "Cache-Control": "no-store" } });
    return Response.json({ providers: [], configured: false, message: "No live provider is connected." }, { status: 503 });
  }
  try {
    const upstream = await fetch(`${apiBase}/v1/providers`, {
      cache: "no-store",
      signal: AbortSignal.timeout(5_000),
    });
    return new Response(await upstream.arrayBuffer(), {
      status: upstream.status,
      headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
    });
  } catch {
    return Response.json(
      { providers: [], configured: false, message: "Live provider health check failed." },
      { status: 503 },
    );
  }
}
