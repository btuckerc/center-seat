import { loadProviderStatuses } from "../../lib/providers.server";

export async function GET() {
  const result = await loadProviderStatuses(5_000);
  if (!result) {
    return Response.json(
      { providers: [], configured: false, message: "Live provider health check failed." },
      { status: 503 },
    );
  }
  return Response.json(result.body, { status: result.status, headers: { "Cache-Control": "no-store" } });
}
