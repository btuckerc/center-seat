import { suggestMovies } from "../../../lib/agent.server.ts";
import { gated, json, problem } from "../_shared.ts";

export async function GET(request: Request) {
  const denied = gated(request); if (denied) return denied;
  try { return json(await suggestMovies(new URL(request.url).searchParams.get("q") ?? "")); }
  catch (error) { return problem(error); }
}
