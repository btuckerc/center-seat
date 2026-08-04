import { SearchExperience } from "./components/SearchExperience";
import { getTrendingMovies } from "./lib/trending.server";

export default async function Home() {
  const trending = await getTrendingMovies();
  return <SearchExperience initialTrending={trending.movies} initialTrendingSourceURL={trending.source_url} />;
}
