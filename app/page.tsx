import { SearchExperience } from "./components/SearchExperience";
import { loadProviderStatuses } from "./lib/providers.server";
import { getTrendingMovies } from "./lib/trending.server";

// Provider status is rendered into the page, so the HTML must never be cached or prerendered.
export const dynamic = "force-dynamic";

// The query service runs beside the web server; a slower status read must not hold the page,
// so the client fetches status itself instead.
const providerStatusBudgetMs = 250;

export default async function Home() {
  const [trending, providers] = await Promise.all([getTrendingMovies(), loadProviderStatuses(providerStatusBudgetMs)]);
  return (
    <SearchExperience
      initialTrending={trending.movies}
      initialTrendingSourceURL={trending.source_url}
      initialProviders={providers ? {
        ok: providers.status >= 200 && providers.status < 300,
        providers: providers.body.providers ?? [],
        checkedAt: providers.checkedAt,
      } : undefined}
    />
  );
}
