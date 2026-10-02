import { searchSeats } from "../../../lib/agent.server.ts";
import { gated, json, problem } from "../_shared.ts";

export async function POST(request: Request) {
  const denied = gated(request); if (denied) return denied;
  try { return json(await searchSeats(await request.json(), new URL(request.url).origin)); }
  catch (error) { return problem(error); }
}
