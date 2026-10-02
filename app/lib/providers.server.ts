import type { ProviderStatus } from "./api";
import { openCinemaProviderStatuses } from "./opencinema";

export type ProviderStatusBody = { providers: ProviderStatus[]; configured?: boolean; message?: string };
export type ProviderStatusResult = { status: number; body: ProviderStatusBody; checkedAt: number };

/**
 * Provider status as `/api/providers` reports it. The home page renders it too, so the first
 * paint already knows which search modes are available. `null` means the query service could
 * not be reached in time.
 */
export async function loadProviderStatuses(timeoutMs: number): Promise<ProviderStatusResult | null> {
  const apiBase = process.env.CENTERSEAT_API_URL?.replace(/\/$/, "");
  if (!apiBase || !/^https?:\/\//.test(apiBase)) {
    const providers = await openCinemaProviderStatuses(timeoutMs);
    if (providers.length) return { status: 200, body: { providers, configured: true }, checkedAt: Date.now() };
    return { status: 503, body: { providers: [], configured: false, message: "No live provider is connected." }, checkedAt: Date.now() };
  }
  try {
    const upstream = await fetch(`${apiBase}/v1/providers`, {
      cache: "no-store",
      signal: AbortSignal.timeout(timeoutMs),
    });
    return { status: upstream.status, body: (await upstream.json()) as ProviderStatusBody, checkedAt: Date.now() };
  } catch {
    return null;
  }
}
