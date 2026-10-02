import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';

const backendState = { query: null };
const server = createServer(async (req, res) => {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString()) : null;
  res.setHeader('Content-Type', 'application/json');
  if (req.url === '/v1/providers') return res.end(JSON.stringify({ providers: [{ kind: 'inventory', configured: true }] }));
  if (req.url === '/v1/seat-queries' && req.method === 'POST') {
    backendState.query = body;
    return res.end(JSON.stringify({ query_id: 'q-1', status: 'complete', expires_at: '2026-10-01T00:00:08Z', refresh_until: '2026-10-01T00:15:00Z', coverage: { range_best_proven: true, winner_verified: true, inventories_fresh: 1, inventories_failed: 0, dates_compared: 2, dates_with_screenings: 2 }, winner: { rank: 1, showtime: { movie_title: 'Dune', venue_name: 'Cinema', auditorium_name: 'Room 1', starts_at: '2026-10-03T01:00:00Z', format: 'IMAX' }, seats: [{ label: 'G7' }, { label: 'G8' }], score: 92, profile_match: 'preferred_zone', verified_at: '2026-10-01T00:00:00Z', booking_url: 'https://tickets.test' }, alternatives: [], warnings: [] }));
  }
  if (req.url?.startsWith('/v1/seat-queries/q-expired/recommendations/')) {
    res.statusCode = 410;
    return res.end(JSON.stringify({ code: 'query_expired', detail: 'The query has expired.' }));
  }
  res.statusCode = 404; res.end(JSON.stringify({ code: 'query_not_found', detail: 'not found' }));
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const { port } = server.address();
process.env.CENTERSEAT_API_URL = `http://127.0.0.1:${port}`;
const { POST: search } = await import('../app/api/agent/search/route.ts');
const { POST: mcp } = await import('../app/api/mcp/route.ts');
const auth = { Authorization: 'Bearer test-token' };
const jsonRequest = (url, data, headers = auth) => new Request(url, { method: 'POST', headers: { ...headers, 'Content-Type': 'application/json' }, body: JSON.stringify(data) });

test('disabled token is hidden and incorrect token is unauthorized', async () => {
  delete process.env.CENTERSEAT_AGENT_TOKEN;
  assert.equal((await search(jsonRequest('http://centerseat.test/api/agent/search', {}))).status, 404);
  process.env.CENTERSEAT_AGENT_TOKEN = 'test-token';
  const response = await search(jsonRequest('http://centerseat.test/api/agent/search', {}, { Authorization: 'Bearer wrong' }));
  assert.equal(response.status, 401);
  assert.equal(response.headers.get('www-authenticate'), 'Bearer');
});

test('search validates explicit timezone/location and returns share URL and checkout handoff', async () => {
  process.env.CENTERSEAT_AGENT_TOKEN = 'test-token';
  for (const body of [{ movie: 'Dune', location: { zip: '10001' } }, { movie: 'Dune', timezone: 'America/New_York' }]) {
    const response = await search(jsonRequest('http://centerseat.test/api/agent/search', body));
    assert.equal(response.status, 400);
    assert.match((await response.json()).detail, /timezone|location/);
  }
  const response = await search(jsonRequest('http://centerseat.test/api/agent/search', { movie: 'Dune', location: { zip: '10001' }, timezone: 'America/New_York' }));
  assert.equal(response.status, 200);
  const result = await response.json();
  assert.equal(backendState.query.time.timezone, 'America/New_York');
  assert.deepEqual(backendState.query.location, { query: '10001', radius_miles: 25 });
  assert.match(result.share_url, /tz=America%2FNew_York/);
  assert.match(result.share_url, /near=10001/);
  assert.deepEqual(result.winner.handoff.seats, ['G7', 'G8']);
  assert.equal(result.winner.handoff.ticket_count, 2);
  assert.equal('seat_map' in result.winner, false);
});

test('MCP initializes, lists and calls tools; expiry is a structured tool error', async () => {
  process.env.CENTERSEAT_AGENT_TOKEN = 'test-token';
  const call = async (method, params, id = 1) => mcp(jsonRequest('http://centerseat.test/api/mcp', { jsonrpc: '2.0', id, method, ...(params === undefined ? {} : { params }) }));
  let response = await call('initialize', { protocolVersion: '2025-03-26' });
  assert.equal((await response.json()).result.protocolVersion, '2025-03-26');
  response = await call('tools/list');
  assert.deepEqual((await response.json()).result.tools.map(tool => tool.name), ['search_seats', 'refresh_recommendation', 'suggest_movies', 'list_providers']);
  response = await call('tools/call', { name: 'search_seats', arguments: { movie: 'Dune', location: { zip: '10001' }, timezone: 'America/New_York' } });
  const success = await response.json();
  assert.equal(success.result.structuredContent.query_id, 'q-1');
  response = await call('tools/call', { name: 'search_seats', arguments: { movie: 'Dune', location: { zip: '10001' } } });
  const invalid = await response.json();
  assert.equal(invalid.result.isError, true);
  assert.match(invalid.result.structuredContent.message, /timezone/);
  response = await call('tools/call', { name: 'refresh_recommendation', arguments: { query_id: 'q-expired', rank: 1, timezone: 'America/New_York' } });
  const expired = await response.json();
  assert.equal(expired.result.isError, true);
  assert.equal(expired.result.structuredContent.code, 'query_expired');
});

test.after(async () => { server.close(); await once(server, 'close'); });
