import { listProviders } from "../../../lib/agent.server.ts";
import { gated, json, problem } from "../_shared.ts";

export async function GET(request: Request) {
  const denied = gated(request); if (denied) return denied;
  try { return json(await listProviders()); }
  catch (error) { return problem(error); }
}
