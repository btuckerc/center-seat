import { AgentError, authorize } from "../../lib/agent.server.ts";

export function gated(request: Request): Response | null {
  const denied = authorize(request);
  return denied ? new Response(denied.body, { status: denied.status, headers: { ...Object.fromEntries(denied.headers), "Cache-Control": "no-store" } }) : null;
}
export function problem(error: unknown): Response {
  const problem = error instanceof AgentError ? error : new AgentError(400, "invalid_request", error instanceof SyntaxError ? "Request body must be valid JSON" : "Invalid request");
  return Response.json({ type: `https://centerseat.invalid/problems/${problem.code}`, title: problem.code, status: problem.status, code: problem.code, detail: problem.message }, { status: problem.status, headers: { "Content-Type": "application/problem+json", "Cache-Control": "no-store" } });
}
export function json(data: unknown): Response { return Response.json(data, { headers: { "Cache-Control": "no-store" } }); }
