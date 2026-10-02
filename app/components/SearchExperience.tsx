"use client";

import { useEffect, useEffectEvent, useRef, useState } from "react";
import { flushSync } from "react-dom";
import {
  browserTimeZone,
  createDefaultQuery,
  isValidTimeZone,
  queryStateToRequest,
  type ProviderStatus,
  type QueryState,
  type SeatQueryResponse,
  type ShowtimeQueryResponse,
  type TrendingMovie,
} from "../lib/api";
import { queryFromSearchParams, searchParamsFromQuery } from "../lib/share";
import { InfoContent } from "./InfoSheet";
import { AlertIcon, ChevronLeftIcon, ChevronRightIcon, FilmIcon, InfoIcon, PencilIcon, PopcornIcon, SearchIcon, SeatIcon, TomatoIcon } from "./icons";
import { locationIsReady, QuerySheet, type LocationMode, type Problem, type ProviderState } from "./QuerySheet";
import { LiveResult, ShowtimeResults } from "./Results";
import { Sheet } from "./Sheet";

type TrendingState = "loading" | "ready" | "unavailable";
type ProviderSnapshot = { ok: boolean; providers: ProviderStatus[]; checkedAt: number };
type SearchExperienceProps = {
  initialTrending?: TrendingMovie[];
  initialTrendingSourceURL?: string;
  initialProviders?: ProviderSnapshot;
};
type SavedPreferences = Pick<QueryState,
  "location" | "timezone" | "tickets" | "profile" | "customSeatZone" | "formats" | "maxDistance" |
  "recliners" | "captions" | "audioDescription" | "wheelchairSpaces" | "companionSeats" |
  "excludeFirstRows" | "allowSplit" | "limitPrice" | "maxPrice"
>;

const preferencesKey = "centerseat.preferences.v3";
const legacyPreferencesKeys = ["centerseat.preferences.v2", "centerseat.preferences.v1"];
const toastMs = 1_800;
// Server-rendered provider status older than this is re-read before it can pick a search endpoint.
const providerStatusFreshMs = 30_000;
// Matches the Fandango provider's showtime-listing cache: a warmed key is re-sent after it lapses.
const discoveryListingTTLMs = 2 * 60_000;

/** Runs a state change inside a view transition when the browser supports it and motion is welcome. */
function withTransition(update: () => void) {
  const doc = document as Document & { startViewTransition?: (callback: () => void) => unknown };
  if (!doc.startViewTransition || window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    update();
    return;
  }
  doc.startViewTransition(() => flushSync(update));
}

function dateSpan(start: string, end: string) {
  const format = (day: string, options: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat("en-US", { ...options, timeZone: "UTC" }).format(new Date(`${day}T12:00:00Z`));
  if (start === end) return format(start, { month: "short", day: "numeric" });
  const sameMonth = start.slice(0, 7) === end.slice(0, 7);
  return `${format(start, { month: "short", day: "numeric" })}–${format(end, sameMonth ? { day: "numeric" } : { month: "short", day: "numeric" })}`;
}

function providerAvailability({ ok, providers }: Omit<ProviderSnapshot, "checkedAt">): { state: ProviderState; locationMode: LocationMode } {
  const usable = (kind: ProviderStatus["kind"]) => providers.find((provider) => provider.kind === kind && provider.configured && provider.status !== "disabled");
  const discovery = usable("discovery");
  const state = ok && discovery && usable("inventory") ? "ready" : ok && discovery ? "discovery" : "unconfigured";
  return { state, locationMode: discovery?.location_mode ?? "coordinates" };
}

export function SearchExperience({
  initialTrending = [],
  initialTrendingSourceURL = "https://www.rottentomatoes.com/browse/movies_in_theaters/sort:popular",
  initialProviders,
}: SearchExperienceProps) {
  const [draft, setDraft] = useState<QueryState>(() => createDefaultQuery());
  const [applied, setApplied] = useState<QueryState | null>(null);
  const [result, setResult] = useState<SeatQueryResponse | null>(null);
  const [showtimeResult, setShowtimeResult] = useState<ShowtimeQueryResponse | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [shareErrors, setShareErrors] = useState<string[]>([]);
  const autoRunPending = useRef(false);
  const [providerState, setProviderState] = useState<ProviderState>(() => initialProviders ? providerAvailability(initialProviders).state : "checking");
  const [locationMode, setLocationMode] = useState<LocationMode>(() => initialProviders ? providerAvailability(initialProviders).locationMode : "coordinates");
  const [searching, setSearching] = useState(false);
  const [locating, setLocating] = useState(false);
  const [locationProblem, setLocationProblem] = useState("");
  const [queryOpen, setQueryOpen] = useState(false);
  const [infoOpen, setInfoOpen] = useState(false);
  const [trending, setTrending] = useState<TrendingMovie[]>(initialTrending);
  const [trendingState, setTrendingState] = useState<TrendingState>(initialTrending.length ? "ready" : "loading");
  const [trendingSourceURL, setTrendingSourceURL] = useState(initialTrendingSourceURL);
  const [poster, setPoster] = useState<{ title: string; url: string; year?: string }>();
  const [resolveToken, setResolveToken] = useState(0);
  const [toast, setToast] = useState<{ id: number; message: string } | null>(null);
  const movieRail = useRef<HTMLDivElement>(null);

  useEffect(() => {
    // Deferred so hydration finishes before URL/preference state replaces the server defaults.
    const timer = window.setTimeout(() => {
      const shared = queryFromSearchParams(new URLSearchParams(window.location.search));
      let saved: Partial<SavedPreferences> = {};
      try {
        const raw = window.localStorage.getItem(preferencesKey) ?? legacyPreferencesKeys.map((key) => window.localStorage.getItem(key)).find(Boolean) ?? null;
        saved = raw ? JSON.parse(raw) as Partial<SavedPreferences> : {};
      } catch {
        window.localStorage.removeItem(preferencesKey);
        for (const key of legacyPreferencesKeys) window.localStorage.removeItem(key);
      }
      // Precedence: shared link > saved preferences > this browser's zone.
      const timezone = shared?.query.timezone ?? (isValidTimeZone(saved.timezone) ? saved.timezone : browserTimeZone());
      const preferences = { ...createDefaultQuery(timezone), ...saved, timezone, movie: "", movieId: undefined, latitude: undefined, longitude: undefined };
      setDraft({ ...preferences, ...shared?.query, timezone });
      setShareErrors(shared?.errors ?? []);
      autoRunPending.current = Boolean(shared?.run);
      setQueryOpen(Boolean(shared));
    }, 0);
    return () => window.clearTimeout(timer);
  }, []);

  useEffect(() => {
    if (initialTrending.length) return;
    let active = true;
    fetch("/api/trending-movies")
      .then(async (response) => response.json() as Promise<{ movies?: TrendingMovie[]; source_url?: string; unavailable?: boolean }>)
      .then((payload) => {
        if (!active) return;
        const movies = payload.movies ?? [];
        setTrending(movies);
        if (payload.source_url) setTrendingSourceURL(payload.source_url);
        setTrendingState(movies.length && !payload.unavailable ? "ready" : "unavailable");
      })
      .catch(() => active && setTrendingState("unavailable"));
    return () => { active = false; };
  }, [initialTrending.length]);

  const providersCheckedAt = useRef(initialProviders?.checkedAt ?? 0);
  const providersRequest = useRef<Promise<void> | null>(null);
  const refreshProviders = useEffectEvent(() => {
    providersRequest.current ??= fetch("/api/providers", { cache: "no-store" })
      .then(async (response) => {
        const payload = (await response.json()) as { providers?: ProviderStatus[] };
        const availability = providerAvailability({ ok: response.ok, providers: payload.providers ?? [] });
        setLocationMode(availability.locationMode);
        setProviderState(availability.state);
      })
      .catch(() => setProviderState("unavailable"))
      .finally(() => {
        providersCheckedAt.current = Date.now();
        providersRequest.current = null;
      });
  });

  useEffect(() => {
    // The server render normally supplies provider status; fetch only when it could not.
    if (!initialProviders) refreshProviders();
  }, [initialProviders]);

  useEffect(() => {
    // A long-open page re-checks status when the search sheet opens or the tab regains focus.
    const refreshIfStale = () => {
      if (Math.abs(Date.now() - providersCheckedAt.current) > providerStatusFreshMs) refreshProviders();
    };
    if (queryOpen) refreshIfStale();
    window.addEventListener("focus", refreshIfStale);
    return () => window.removeEventListener("focus", refreshIfStale);
  }, [queryOpen]);

  // Discovery inputs already warmed, and when; a key is re-sent after the provider's listing TTL.
  const prefetchedDiscovery = useRef({ key: "", at: 0 });
  useEffect(() => {
    // Warm the provider's showtime listing while the search is still being composed. Seat maps are
    // never prefetched: the search reads them fresh.
    const discoveryReady = queryOpen && !searching && (providerState === "ready" || providerState === "discovery") && draft.movieId &&
      locationIsReady(draft, locationMode) && draft.formats.length > 0 && isValidTimeZone(draft.timezone) && draft.dateEnd >= draft.dateStart;
    if (!discoveryReady) return;
    const request = queryStateToRequest(draft);
    const key = JSON.stringify([request.movie_id, request.location, request.dates, request.time.timezone]);
    if (key === prefetchedDiscovery.current.key && Date.now() - prefetchedDiscovery.current.at < discoveryListingTTLMs) return;
    const timer = window.setTimeout(() => {
      prefetchedDiscovery.current = { key, at: Date.now() };
      void fetch("/api/discovery-prefetch", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(request), keepalive: true })
        .then((response) => response.ok)
        .catch(() => false)
        .then((warmed) => {
          if (!warmed && prefetchedDiscovery.current.key === key) prefetchedDiscovery.current = { key: "", at: 0 };
        });
    }, 500);
    return () => window.clearTimeout(timer);
  }, [draft, locationMode, providerState, queryOpen, searching]);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(null), toastMs);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const notify = (message: string) => setToast({ id: Date.now(), message });

  const openQuery = (movie?: TrendingMovie, image?: HTMLImageElement | null) => {
    if (!movie) {
      setProblem(null);
      setQueryOpen(true);
      return;
    }
    // The poster morphs into the sheet's title row: name it for the old snapshot, unname it for the new one.
    if (image) image.style.viewTransitionName = "active-poster";
    withTransition(() => {
      if (image) image.style.viewTransitionName = "";
      setProblem(null);
      setPoster({ title: movie.title, url: movie.poster_url, year: movie.release_text.match(/\b(?:19|20)\d{2}\b/)?.[0] });
      setDraft((current) => ({ ...current, movie: movie.title, movieId: undefined }));
      setResolveToken((token) => token + 1);
      setQueryOpen(true);
    });
  };

  useEffect(() => {
    // "/" opens search from anywhere outside a text field.
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey || queryOpen || infoOpen) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable='true']")) return;
      event.preventDefault();
      setProblem(null);
      setQueryOpen(true);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [queryOpen, infoOpen]);

  const resetHome = () => withTransition(() => {
    setResult(null);
    setShowtimeResult(null);
    setProblem(null);
    setApplied(null);
  });

  const useCurrentLocation = () => {
    if (!navigator.geolocation) {
      setLocationProblem("This browser does not share location.");
      return;
    }
    setLocating(true);
    setLocationProblem("");
    navigator.geolocation.getCurrentPosition(
      (position) => {
        setDraft((current) => ({ ...current, location: "Current location", latitude: position.coords.latitude, longitude: position.coords.longitude }));
        setLocating(false);
      },
      () => {
        setLocationProblem(locationMode === "text_or_coordinates" ? "Location blocked — type a ZIP, city, or address." : locationMode === "postal_or_coordinates" ? "Location blocked — a ZIP works too." : "This source needs your location.");
        setLocating(false);
      },
      { enableHighAccuracy: false, timeout: 10_000, maximumAge: 3_600_000 },
    );
  };

  const persistPreferences = (state: QueryState) => {
    const typedLocation = (locationMode === "text_or_coordinates" || locationMode === "postal_or_coordinates") && locationIsReady({ location: state.location }, locationMode);
    const preferences: SavedPreferences = {
      location: typedLocation ? state.location.trim() : "",
      timezone: state.timezone,
      tickets: state.tickets,
      profile: state.profile,
      customSeatZone: state.customSeatZone,
      formats: state.formats,
      maxDistance: state.maxDistance,
      recliners: state.recliners,
      captions: state.captions,
      audioDescription: state.audioDescription,
      wheelchairSpaces: state.wheelchairSpaces,
      companionSeats: state.companionSeats,
      excludeFirstRows: state.excludeFirstRows,
      allowSplit: state.allowSplit,
      limitPrice: state.limitPrice,
      maxPrice: state.maxPrice,
    };
    window.localStorage.setItem(preferencesKey, JSON.stringify(preferences));
  };

  const search = async (state: QueryState = draft) => {
    if ((providerState !== "ready" && providerState !== "discovery") || searching) return;
    if (!isValidTimeZone(state.timezone)) {
      setProblem({ title: "Invalid timezone", detail: "Enter a valid IANA timezone." });
      setQueryOpen(true);
      return;
    }
    if (!state.movie.trim() || !locationIsReady(state, locationMode) || !state.formats.length) {
      setQueryOpen(true);
      return;
    }
    if (state.dateEnd < state.dateStart) {
      setProblem({ title: "Invalid dates", detail: "The end date is before the start date." });
      setQueryOpen(true);
      return;
    }
    setSearching(true);
    setProblem(null);
    const searchState = { ...state };
    try {
      const endpoint = providerState === "ready" ? "/api/seat-queries" : "/api/showtime-queries";
      const response = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
        body: JSON.stringify(queryStateToRequest(searchState)),
      });
      const body = await response.json();
      if (!response.ok) {
        setProblem({ title: body.title ?? "Search failed", detail: body.detail ?? "The live source returned no usable response.", status: response.status });
        setQueryOpen(true);
        return;
      }
      persistPreferences(searchState);
      const url = new URL(window.location.href);
      url.search = searchParamsFromQuery(searchState).toString();
      window.history.replaceState(null, "", url);
      withTransition(() => {
        setApplied(searchState);
        if (providerState === "ready") {
          setResult(body as SeatQueryResponse);
          setShowtimeResult(null);
        } else {
          setShowtimeResult(body as ShowtimeQueryResponse);
          setResult(null);
        }
        setQueryOpen(false);
      });
    } catch {
      setProblem({ title: "Source unreachable", detail: "No substitute result was returned." });
      setQueryOpen(true);
    } finally {
      setSearching(false);
    }
  };

  const runSharedSearch = useEffectEvent(() => {
    autoRunPending.current = false;
    const url = new URL(window.location.href);
    url.searchParams.delete("run");
    window.history.replaceState(null, "", url);
    void search();
  });

  useEffect(() => {
    // A `run=1` link runs its read-only search once, after hydration fills `draft` and provider status resolves.
    if (autoRunPending.current && providerState !== "checking") runSharedSearch();
  }, [providerState, draft]);

  const hasOutcome = Boolean(result || showtimeResult);
  const place = result?.resolved_location?.label ?? showtimeResult?.resolved_location?.label ?? applied?.location;
  const noMatch = result && !result.winner ? result : null;
  const scrollRail = (direction: 1 | -1) => {
    const rail = movieRail.current;
    if (!rail) return;
    rail.scrollBy({ left: direction * rail.clientWidth * .8, behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  };
  const rerun = (state: QueryState) => {
    // Reuse the locked query sheet as the progress surface so a rerun is never silent.
    setDraft(state);
    setQueryOpen(true);
    void search(state);
  };

  return (
    <main className={`app ${hasOutcome ? "is-result" : "is-lobby"}`}>
      <header className="topbar">
        <button aria-label="CenterSeat home" className="brand" onClick={resetHome} type="button">
          <span aria-hidden="true" className="brand-mark"><SeatIcon size={18} /></span>
          <span className="brand-name">CenterSeat</span>
        </button>
        {hasOutcome && applied ? (
          <button aria-label="Edit search" className="query-chip" onClick={() => openQuery()} type="button">
            <b>{applied.movie.replace(/\s*\((?:19|20)\d{2}\)\s*$/, "")}</b>
            <span title={place}>{place}</span>
            <span>{dateSpan(applied.dateStart, applied.dateEnd)}</span>
            <span className="query-chip-seats"><SeatIcon size={14} />{applied.tickets}</span>
            <PencilIcon className="query-chip-edit" size={16} />
          </button>
        ) : null}
        <div className="topbar-tools">
          {hasOutcome ? <button aria-label="Search" className="icon-button" onClick={() => openQuery()} type="button"><SearchIcon /></button> : null}
          {!hasOutcome ? <button aria-label="How it works" className="icon-button" onClick={() => setInfoOpen(true)} type="button"><InfoIcon /></button> : null}
        </div>
      </header>

      {!hasOutcome ? (
        <section aria-label="Choose a movie" className="lobby">
          <div className="lobby-hero">
            <h1>The best seat, every showtime.</h1>
            <button className="search-pill" onClick={() => openQuery()} type="button">
              <SearchIcon size={22} />
              <span>Search movies</span>
              <kbd aria-hidden="true">/</kbd>
            </button>
          </div>

          <div className="movie-rail">
            <div className="rail-head">
              <a href={trendingSourceURL} rel="noreferrer" target="_blank">Popular on Rotten Tomatoes</a>
              <span className="rail-arrows">
                <button aria-label="Previous movies" className="icon-button" onClick={() => scrollRail(-1)} type="button"><ChevronLeftIcon /></button>
                <button aria-label="Next movies" className="icon-button" onClick={() => scrollRail(1)} type="button"><ChevronRightIcon /></button>
              </span>
            </div>
            <div className="posters" ref={movieRail}>
              {trendingState === "loading" ? Array.from({ length: 6 }, (_, index) => <div aria-hidden="true" className="poster is-skeleton" key={index}><span className="poster-art" /></div>) : null}
              {trending.map((movie, index) => (
                <button
                  className="poster"
                  key={movie.id}
                  onClick={(event) => openQuery(movie, event.currentTarget.querySelector("img"))}
                  style={{ "--i": index } as React.CSSProperties}
                  title={movie.release_text}
                  type="button"
                >
                  <span className="poster-art">
                    {/* eslint-disable-next-line @next/next/no-img-element -- provider poster hosts are dynamic. */}
                    <img
                      alt=""
                      decoding="async"
                      fetchPriority={index < 2 ? "high" : "auto"}
                      height="305"
                      loading={index < 5 ? "eager" : "lazy"}
                      src={movie.poster_url}
                      width="206"
                    />
                  </span>
                  <strong>{movie.title}</strong>
                  <span className="scores">
                    {movie.critics_score !== undefined ? <span aria-label={`${movie.certified_fresh ? "Certified fresh, critics" : "Critics"} ${movie.critics_score}%`}><TomatoIcon size={15} />{movie.critics_score}%</span> : null}
                    {movie.audience_score !== undefined ? <span aria-label={`Audience ${movie.audience_score}%`}><PopcornIcon size={15} />{movie.audience_score}%</span> : null}
                  </span>
                </button>
              ))}
              {trendingState === "unavailable" ? (
                <button className="poster is-empty" onClick={() => openQuery()} type="button">
                  <span className="poster-art"><FilmIcon size={32} /></span>
                  <strong>Search any title</strong>
                </button>
              ) : null}
            </div>
          </div>
        </section>
      ) : (
        <section aria-live="polite" className="results">
          {result?.winner && applied ? <LiveResult key={result.query_id} applied={applied} notify={notify} onRerun={() => rerun(applied)} result={result} /> : null}
          {showtimeResult && applied ? <ShowtimeResults applied={applied} notify={notify} result={showtimeResult} /> : null}
          {noMatch ? (
            <div className="empty">
              <AlertIcon size={28} />
              <h1>{(noMatch.coverage.inventories_failed ?? noMatch.coverage.providers_degraded ?? 0) > 0 ? "No match yet — some seat maps didn't load." : "No seat matched every filter."}</h1>
              <div className="empty-actions">
                <button className="search-button" onClick={() => openQuery()} type="button"><PencilIcon size={18} /><span>Adjust</span></button>
                <button aria-label="Details" className="icon-button" onClick={() => setInfoOpen(true)} type="button"><InfoIcon /></button>
              </div>
            </div>
          ) : null}
        </section>
      )}

      <Sheet className="query-sheet" label="Search" locked={searching} onClose={() => setQueryOpen(false)} open={queryOpen}>
        <QuerySheet
          draft={draft}
          locating={locating}
          locationMode={locationMode}
          locationProblem={locationProblem}
          onLocate={useCurrentLocation}
          onLocationEdit={() => setLocationProblem("")}
          onSubmit={() => void search()}
          poster={poster}
          problem={problem}
          providerState={providerState}
          resolveToken={resolveToken}
          searching={searching}
          setDraft={setDraft}
          shareErrors={shareErrors}
        />
      </Sheet>

      <Sheet className="info-sheet" label={noMatch ? "Details" : "How it works"} onClose={() => setInfoOpen(false)} open={infoOpen}>
        <InfoContent coverage={noMatch?.coverage} />
      </Sheet>

      {toast ? <div className="toast" key={toast.id} role="status">{toast.message}</div> : null}
    </main>
  );
}
