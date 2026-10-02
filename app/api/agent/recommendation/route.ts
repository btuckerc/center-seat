import { refreshRecommendation } from "../../../lib/agent.server.ts";
import { gated, json, problem } from "../_shared.ts";

export async function GET(request: Request) {
  const denied = gated(request); if (denied) return denied;
  try {
    const params = new URL(request.url).searchParams;
    const rank = Number(params.get("rank"));
    return json(await refreshRecommendation({ query_id: params.get("query_id") ?? "", rank, timezone: params.get("tz") ?? "" }));
  } catch (error) { return problem(error); }
}
