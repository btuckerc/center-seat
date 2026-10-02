import { AgentError, authorize, listProviders, refreshRecommendation, searchSeats, suggestMovies } from "../../lib/agent.server.ts";

const headers = { "Cache-Control": "no-store" };
const instructions = "CenterSeat is read-only: it finds and verifies movie seats but never holds, reserves, or buys them. Handoff names the exact seats and provider checkout URL. Select only those exact seats and stop before payment; never substitute seats. Results expire quickly; refresh a recommendation before acting and rerun the search when refresh_until has passed.";
const tools = [
  { name: "search_seats", description: "Find movie seats near an explicit location and in an explicit IANA timezone. location.query accepts a ZIP, city, neighborhood, or street address; prefer a ZIP when a city name is ambiguous. Check resolved_location in the result to confirm the matched place. CenterSeat never holds or buys seats; any checkout handoff requires the exact seats and stopping before payment without substitutions.", inputSchema: { type: "object", additionalProperties: false, required: ["movie", "location", "timezone"], properties: { movie: { type: "string" }, movie_id: { type: "string" }, timezone: { type: "string", description: "Required IANA timezone, e.g. America/Los_Angeles." }, location: { type: "object", description: "Explicit place: zip or query, or latitude and longitude. query may be a ZIP, city, neighborhood, or street address.", properties: { zip: { type: "string" }, query: { type: "string", description: "Free-text ZIP, city, neighborhood, or street address. Prefer ZIP when a city is ambiguous." }, latitude: { type: "number" }, longitude: { type: "number" } } }, dates: { type: "object" }, tickets: { type: "integer" }, profile: { type: "string" }, time: { type: "object" }, formats: { type: "array", items: { type: "string" } }, max_distance_miles: { type: "number" }, max_total_price: { type: "number" }, captions: { type: "string" }, accessibility: { type: "object" } } } },
  { name: "refresh_recommendation", description: "Refresh one exact recommendation while it remains refreshable. Never hold or buy; at checkout choose only the returned exact seats and stop before payment, never substitute.", inputSchema: { type: "object", additionalProperties: false, required: ["query_id", "rank", "timezone"], properties: { query_id: { type: "string" }, rank: { type: "integer", minimum: 1 }, timezone: { type: "string", description: "IANA timezone for displayed local time." } } } },
  { name: "suggest_movies", description: "Suggest movie titles matching a query.", inputSchema: { type: "object", additionalProperties: false, required: ["q"], properties: { q: { type: "string" } } } },
  { name: "list_providers", description: "List configured discovery, inventory, and booking-link providers.", inputSchema: { type: "object", additionalProperties: false, properties: {} } },
];
function rpc(id: unknown, result: unknown) { return Response.json({ jsonrpc: "2.0", id, result }, { headers }); }
function rpcError(id: unknown, code: number, message: string) { return Response.json({ jsonrpc: "2.0", id, error: { code, message } }, { headers }); }
function resultContent(value: unknown) { const text = JSON.stringify(value); return { content: [{ type: "text", text }], structuredContent: value }; }

export async function POST(request: Request) {
  const publicOrigin = process.env.CENTERSEAT_PUBLIC_URL || new URL(request.url).origin;
  const origin = request.headers.get("Origin");
  if (origin && origin !== publicOrigin) return Response.json({ type: "about:blank", title: "Forbidden", status: 403, code: "origin_forbidden", detail: "Origin is not allowed." }, { status: 403, headers: { ...headers, "Content-Type": "application/problem+json" } });
  const denied = authorize(request); if (denied) return denied;
  let message: unknown;
  try { message = await request.json(); } catch { return rpcError(null, -32700, "Parse error"); }
  if (!message || typeof message !== "object" || Array.isArray(message)) return rpcError(null, -32600, "Invalid Request");
  const req = message as Record<string, unknown>;
  if (req.jsonrpc !== "2.0" || typeof req.method !== "string" || Array.isArray(message)) return rpcError(req.id ?? null, -32600, "Invalid Request");
  if (req.method.startsWith("notifications/")) return new Response(null, { status: 202, headers });
  const id = req.id;
  if (id === undefined) return rpcError(null, -32600, "Request id is required");
  if (req.method === "initialize") {
    const params = req.params && typeof req.params === "object" ? req.params as Record<string, unknown> : {};
    const versions = ["2025-06-18", "2025-03-26"];
    const version = typeof params.protocolVersion === "string" && versions.includes(params.protocolVersion) ? params.protocolVersion : versions[0];
    return rpc(id, { protocolVersion: version, capabilities: { tools: {} }, serverInfo: { name: "centerseat", version: "1.0.0" }, instructions });
  }
  if (req.method === "ping") return rpc(id, {});
  if (req.method === "tools/list") return rpc(id, { tools });
  if (req.method === "tools/call") {
    const params = req.params && typeof req.params === "object" ? req.params as Record<string, unknown> : {};
    if (typeof params.name !== "string") return rpcError(id, -32602, "tools/call requires a tool name");
    const args = params.arguments ?? {};
    try {
      let value: unknown;
      switch (params.name) {
        case "search_seats": value = await searchSeats(args, request.url); break;
        case "refresh_recommendation": {
          if (!args || typeof args !== "object") throw new AgentError(400, "invalid_request", "arguments must be an object");
          const a = args as Record<string, unknown>;
          value = await refreshRecommendation({ query_id: typeof a.query_id === "string" ? a.query_id : "", rank: typeof a.rank === "number" ? a.rank : NaN, timezone: typeof a.timezone === "string" ? a.timezone : "" }); break;
        }
        case "suggest_movies": {
          if (!args || typeof args !== "object" || typeof (args as Record<string, unknown>).q !== "string") throw new AgentError(400, "invalid_request", "q is required");
          value = await suggestMovies((args as {q: string}).q); break;
        }
        case "list_providers": value = await listProviders(); break;
        default: return rpcError(id, -32602, `Unknown tool: ${params.name}`);
      }
      return rpc(id, resultContent(value));
    } catch (caught) {
      const e = caught instanceof AgentError ? caught : new AgentError(500, "internal_error", "The tool failed unexpectedly");
      const problem = { code: e.code, status: e.status, message: e.message };
      return rpc(id, { ...resultContent(problem), isError: true });
    }
  }
  return rpcError(id, -32601, `Method not found: ${req.method}`);
}
export async function GET() { return new Response(null, { status: 405, headers: { ...headers, Allow: "POST" } }); }
export async function DELETE() { return new Response(null, { status: 405, headers: { ...headers, Allow: "POST" } }); }
