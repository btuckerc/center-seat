"use client";

import { FormEvent, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, useEffect, useEffectEvent, useId, useRef, useState } from "react";
import {
  browserTimeZone,
  buildCheckoutHandoff,
  createDefaultQuery,
  formatShowtime,
  handoffText,
  isValidTimeZone,
  queryStateToRequest,
  type MovieSuggestion,
  type NormalizedSeatZone,
  type ProviderStatus,
  type QueryState,
  type Recommendation,
  type SeatProfile,
  type SeatQueryResponse,
  type ShowtimeQueryResponse,
  type TimeMode,
  type TrendingMovie,
} from "../lib/api";
import { queryFromSearchParams, searchParamsFromQuery } from "../lib/share";
import { SeatMap } from "./SeatMap";

const formatOptions = ["Standard", "Dolby", "IMAX", "XD", "ScreenX", "3D"];
const timeModes: { value: TimeMode; label: string }[] = [
  { value: "any", label: "Any time" },
  { value: "inside", label: "Inside this window" },
  { value: "outside", label: "Outside this window" },
  { value: "before", label: "Before this time" },
  { value: "after", label: "After this time" },
];
const profiles: { value: SeatProfile; label: string; hint: string }[] = [
  { value: "dead_center", label: "Dead center", hint: "Exact geometric middle" },
  { value: "balanced", label: "Best overall", hint: "Center + ideal depth" },
  { value: "two_thirds_back", label: "2/3 back", hint: "Classic viewing zone" },
  { value: "aisle", label: "Near an aisle", hint: "Easy in and out" },
  { value: "front", label: "Closer", hint: "Immersive front section" },
  { value: "back", label: "Back rows", hint: "More distance" },
  { value: "custom", label: "Custom zone", hint: "Draw a preferred area" },
];

type ProviderState = "checking" | "ready" | "discovery" | "unconfigured" | "unavailable";
type TrendingState = "loading" | "ready" | "unavailable";
type Problem = { title: string; detail: string; status?: number };
type SearchExperienceProps = {
  initialTrending?: TrendingMovie[];
  initialTrendingSourceURL?: string;
};
type SavedPreferences = Pick<QueryState,
  "location" | "timezone" | "tickets" | "profile" | "customSeatZone" | "formats" | "maxDistance" |
  "recliners" | "captions" | "audioDescription" | "wheelchairSpaces" | "companionSeats" |
  "excludeFirstRows" | "allowSplit" | "limitPrice" | "maxPrice"
>;

const preferencesKey = "centerseat.preferences.v3";
const legacyPreferencesKeys = ["centerseat.preferences.v2", "centerseat.preferences.v1"];
const postalCodePattern = /^\s*\d{5}(?:-\d{4})?\s*$/;
// Module-level clock reads: event handlers may consult wall time; render reads the ticking `now` state.
const currentTime = () => Date.now();
const isPast = (iso: string) => currentTime() >= new Date(iso).getTime();

function FieldLabel({ children }: { children: React.ReactNode }) {
  return <span className="field-label">{children}</span>;
}

const defaultCustomZone: NormalizedSeatZone = { minimumX: .28, maximumX: .72, minimumY: .42, maximumY: .76 };
const clampUnit = (value: number) => Math.max(0, Math.min(1, value));
const roundCoordinate = (value: number) => Math.round(clampUnit(value) * 1_000) / 1_000;

function CustomSeatZonePicker({ value, onChange }: { value: NormalizedSeatZone; onChange: (zone: NormalizedSeatZone) => void }) {
  const surface = useRef<HTMLDivElement>(null);
  const dragOrigin = useRef<{ x: number; y: number } | null>(null);

  const pointFromEvent = (event: ReactPointerEvent<HTMLDivElement>) => {
    const bounds = surface.current?.getBoundingClientRect();
    if (!bounds) return null;
    return {
      x: clampUnit((event.clientX - bounds.left) / bounds.width),
      y: clampUnit((event.clientY - bounds.top) / bounds.height),
    };
  };

  const zoneFromPoints = (start: { x: number; y: number }, end: { x: number; y: number }): NormalizedSeatZone => {
    const axis = (first: number, second: number) => {
      let minimum = Math.min(first, second);
      let maximum = Math.max(first, second);
      if (maximum - minimum < .08) {
        const center = (minimum + maximum) / 2;
        minimum = Math.max(0, center - .04);
        maximum = Math.min(1, minimum + .08);
        minimum = Math.max(0, maximum - .08);
      }
      return [roundCoordinate(minimum), roundCoordinate(maximum)] as const;
    };
    const [minimumX, maximumX] = axis(start.x, end.x);
    const [minimumY, maximumY] = axis(start.y, end.y);
    return { minimumX, maximumX, minimumY, maximumY };
  };

  const draw = (event: ReactPointerEvent<HTMLDivElement>) => {
    const point = pointFromEvent(event);
    if (!point || !dragOrigin.current) return;
    onChange(zoneFromPoints(dragOrigin.current, point));
  };

  const moveWithKeyboard = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? .05 : .025;
    let deltaX = 0;
    let deltaY = 0;
    if (event.key === "ArrowLeft") deltaX = -step;
    else if (event.key === "ArrowRight") deltaX = step;
    else if (event.key === "ArrowUp") deltaY = -step;
    else if (event.key === "ArrowDown") deltaY = step;
    else return;
    event.preventDefault();
    const width = value.maximumX - value.minimumX;
    const height = value.maximumY - value.minimumY;
    const minimumX = clampUnit(Math.min(1 - width, value.minimumX + deltaX));
    const minimumY = clampUnit(Math.min(1 - height, value.minimumY + deltaY));
    onChange({
      minimumX: roundCoordinate(minimumX), maximumX: roundCoordinate(minimumX + width),
      minimumY: roundCoordinate(minimumY), maximumY: roundCoordinate(minimumY + height),
    });
  };

  return (
    <section className="custom-zone-field full-span" aria-labelledby="custom-zone-title">
      <div className="custom-zone-head">
        <span><b id="custom-zone-title">Draw your preferred area</b><small>The shape scales to each auditorium.</small></span>
        <button onClick={() => onChange(defaultCustomZone)} type="button">Reset</button>
      </div>
      <div className="custom-zone-screen" aria-hidden="true"><span>SCREEN</span><i /></div>
      <div
        aria-label="Custom seat area. Drag to redraw it. Use arrow keys to move it."
        className="custom-zone-surface"
        onKeyDown={moveWithKeyboard}
        onPointerCancel={() => { dragOrigin.current = null; }}
        onPointerDown={(event) => {
          const point = pointFromEvent(event);
          if (!point) return;
          event.currentTarget.setPointerCapture(event.pointerId);
          dragOrigin.current = point;
          onChange(zoneFromPoints(point, point));
        }}
        onPointerMove={draw}
        onPointerUp={(event) => {
          draw(event);
          dragOrigin.current = null;
          if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
        }}
        ref={surface}
        role="group"
        tabIndex={0}
      >
        <span className="custom-zone-depth front" aria-hidden="true">FRONT</span>
        <span className="custom-zone-depth back" aria-hidden="true">BACK</span>
        <span
          className="custom-zone-selection"
          style={{
            left: `${value.minimumX * 100}%`,
            top: `${value.minimumY * 100}%`,
            width: `${(value.maximumX - value.minimumX) * 100}%`,
            height: `${(value.maximumY - value.minimumY) * 100}%`,
          }}
        ><b>Preferred</b></span>
      </div>
      <p>Drag to redraw. Arrow keys move the selected area.</p>
    </section>
  );
}


const humanize = (value: string) => value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
const normalizeTitle = (value: string) => value.toLocaleLowerCase().replace(/[^a-z0-9]+/g, "").trim();

const timezoneOptions: string[] = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];

export function SearchExperience({
  initialTrending = [],
  initialTrendingSourceURL = "https://www.rottentomatoes.com/browse/movies_in_theaters/sort:popular",
}: SearchExperienceProps) {
  const [draft, setDraft] = useState<QueryState>(() => createDefaultQuery());
  const [applied, setApplied] = useState<QueryState | null>(null);
  const [result, setResult] = useState<SeatQueryResponse | null>(null);
  const [showtimeResult, setShowtimeResult] = useState<ShowtimeQueryResponse | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [shareErrors, setShareErrors] = useState<string[]>([]);
  const [copyNotice, setCopyNotice] = useState("");
  const autoRunPending = useRef(false);
  const [providerState, setProviderState] = useState<ProviderState>("checking");
  const [postalLocationSupported, setPostalLocationSupported] = useState(false);
  const [searching, setSearching] = useState(false);
  const [locating, setLocating] = useState(false);
  const [locationProblem, setLocationProblem] = useState("");
  const [queryOpen, setQueryOpen] = useState(false);
  const [infoOpen, setInfoOpen] = useState(false);
  const [trending, setTrending] = useState<TrendingMovie[]>(initialTrending);
  const [trendingState, setTrendingState] = useState<TrendingState>(initialTrending.length ? "ready" : "loading");
  const [trendingSourceURL, setTrendingSourceURL] = useState(initialTrendingSourceURL);
  const [movieSuggestions, setMovieSuggestions] = useState<MovieSuggestion[]>([]);
  const [suggestionsOpen, setSuggestionsOpen] = useState(false);
  const [activeSuggestion, setActiveSuggestion] = useState(-1);
  const movieRail = useRef<HTMLDivElement>(null);
  const suggestionsID = useId();

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

  useEffect(() => {
    let active = true;
    fetch("/api/providers", { cache: "no-store" })
      .then(async (response) => {
        const payload = (await response.json()) as { providers?: ProviderStatus[] };
        if (!active) return;
        const statuses = payload.providers ?? [];
        const discoveryConfigured = statuses.some((provider) => provider.kind === "discovery" && provider.configured && provider.status !== "disabled");
        const inventoryConfigured = statuses.some((provider) => provider.kind === "inventory" && provider.configured && provider.status !== "disabled");
        setPostalLocationSupported(statuses.some((provider) => provider.kind === "discovery" && provider.configured && provider.status !== "disabled" && provider.location_mode === "postal_or_coordinates"));
        if (response.ok && discoveryConfigured && inventoryConfigured) setProviderState("ready");
        else if (response.ok && discoveryConfigured) setProviderState("discovery");
        else setProviderState("unconfigured");
      })
      .catch(() => active && setProviderState("unavailable"));
    return () => { active = false; };
  }, []);

  useEffect(() => {
    const query = draft.movie.trim();
    if (providerState !== "ready" || draft.movieId || query.length < 2) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      fetch(`/api/movie-suggestions?q=${encodeURIComponent(query)}`, { signal: controller.signal, cache: "no-store" })
        .then(async (response) => response.ok ? response.json() as Promise<{ suggestions?: MovieSuggestion[] }> : { suggestions: [] })
        .then((payload) => {
          const suggestions = payload.suggestions ?? [];
          setMovieSuggestions(suggestions);
          setSuggestionsOpen(suggestions.length > 0);
          setActiveSuggestion(-1);
        })
        .catch(() => undefined);
    }, 220);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [draft.movie, draft.movieId, providerState]);

  useEffect(() => {
    const modalOpen = queryOpen || infoOpen;
    document.body.classList.toggle("modal-open", modalOpen);
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (infoOpen) setInfoOpen(false);
      else if (queryOpen && !searching) setQueryOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      document.body.classList.remove("modal-open");
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [infoOpen, queryOpen, searching]);

  const update = <K extends keyof QueryState>(key: K, value: QueryState[K]) => setDraft((current) => ({ ...current, [key]: value }));

  const chooseMovie = (suggestion: MovieSuggestion) => {
    setDraft((current) => ({ ...current, movie: suggestion.title, movieId: suggestion.id }));
    setMovieSuggestions([]);
    setSuggestionsOpen(false);
    setActiveSuggestion(-1);
  };

  const resolveCanonicalMovie = async (title: string) => {
    if (providerState !== "ready") return;
    try {
      const response = await fetch(`/api/movie-suggestions?q=${encodeURIComponent(title)}`, { cache: "no-store" });
      if (!response.ok) return;
      const payload = await response.json() as { suggestions?: MovieSuggestion[] };
      const suggestions = payload.suggestions ?? [];
      const exact = suggestions.find((suggestion) => normalizeTitle(suggestion.title) === normalizeTitle(title)) ?? suggestions[0];
      if (exact) setDraft((current) => current.movie === title ? { ...current, movie: exact.title, movieId: exact.id } : current);
    } catch {
      // Free-text querying remains available if canonical resolution is unavailable.
    }
  };

  const openQuery = (movie?: TrendingMovie) => {
    setProblem(null);
    if (movie) {
      setDraft((current) => ({ ...current, movie: movie.title, movieId: undefined }));
      void resolveCanonicalMovie(movie.title);
    }
    setQueryOpen(true);
  };

  const resetHome = () => {
    setResult(null);
    setShowtimeResult(null);
    setProblem(null);
    setApplied(null);
  };

  const toggleFormat = (format: string) => {
    setDraft((current) => ({
      ...current,
      formats: current.formats.includes(format) ? current.formats.filter((item) => item !== format) : [...current.formats, format],
    }));
  };

  const useCurrentLocation = () => {
    if (!navigator.geolocation) {
      setLocationProblem("This browser does not expose location.");
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
        setLocationProblem(postalLocationSupported ? "Location was not granted. A ZIP works just as well." : "This provider requires location coordinates.");
        setLocating(false);
      },
      { enableHighAccuracy: false, timeout: 10_000, maximumAge: 3_600_000 },
    );
  };

  const hasCoordinates = draft.latitude !== undefined && draft.longitude !== undefined;
  const hasPostalLocation = postalLocationSupported && postalCodePattern.test(draft.location);
  const locationReady = hasCoordinates || hasPostalLocation;

  const persistPreferences = () => {
    const preferences: SavedPreferences = {
      location: hasPostalLocation ? draft.location.trim() : "",
      timezone: draft.timezone,
      tickets: draft.tickets,
      profile: draft.profile,
      customSeatZone: draft.customSeatZone,
      formats: draft.formats,
      maxDistance: draft.maxDistance,
      recliners: draft.recliners,
      captions: draft.captions,
      audioDescription: draft.audioDescription,
      wheelchairSpaces: draft.wheelchairSpaces,
      companionSeats: draft.companionSeats,
      excludeFirstRows: draft.excludeFirstRows,
      allowSplit: draft.allowSplit,
      limitPrice: draft.limitPrice,
      maxPrice: draft.maxPrice,
    };
    window.localStorage.setItem(preferencesKey, JSON.stringify(preferences));
  };

  const search = async (event?: FormEvent, state: QueryState = draft) => {
    event?.preventDefault();
    const stateHasLocation = state.latitude !== undefined && state.longitude !== undefined || postalLocationSupported && postalCodePattern.test(state.location);
    if ((providerState !== "ready" && providerState !== "discovery") || searching) return;
    if (!isValidTimeZone(state.timezone)) {
      setProblem({ title: "Invalid timezone", detail: "Enter a valid IANA timezone before searching." });
      return;
    }
    if (!state.movie.trim() || !state.location.trim() || !stateHasLocation || !state.formats.length) return;
    if (state.dateEnd < state.dateStart) {
      setProblem({ title: "Invalid date range", detail: "The end date must be on or after the start date." });
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
        setProblem({ title: body.title ?? "Live query failed", detail: body.detail ?? "The live source did not return a usable response.", status: response.status });
      } else {
        setApplied(searchState);
        if (providerState === "ready") {
          setResult(body as SeatQueryResponse);
          setShowtimeResult(null);
        } else {
          setShowtimeResult(body as ShowtimeQueryResponse);
          setResult(null);
        }
        persistPreferences();
        const url = new URL(window.location.href);
        url.search = searchParamsFromQuery(searchState).toString();
        window.history.replaceState(null, "", url);
        setQueryOpen(false);
      }
    } catch {
      setProblem({ title: "Live query unavailable", detail: "CenterSeat could not reach the configured source. No substitute result was returned." });
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

  const copyShareLink = async () => {
    if (!applied) return;
    const url = new URL(window.location.href);
    url.search = searchParamsFromQuery(applied).toString();
    try {
      await navigator.clipboard.writeText(url.toString());
      setCopyNotice("Search link copied.");
    } catch {
      window.prompt("Copy this search link", url.toString());
      setCopyNotice("Copy the link from the dialog.");
    }
  };

  const providerText = {
    checking: "Checking live sources",
    ready: "Live seats connected",
    discovery: "Showtimes connected",
    unconfigured: "Source required",
    unavailable: "Source unavailable",
  }[providerState];
  const hasOutcome = Boolean(result || showtimeResult || problem);
  const searchText = searching ? "Checking live seats…" : providerState === "ready" ? "Find my best seat" : providerState === "discovery" ? "Find showtimes" : "Live source required";
  const showStartTime = draft.timeMode === "inside" || draft.timeMode === "outside" || draft.timeMode === "after";
  const showEndTime = draft.timeMode === "inside" || draft.timeMode === "outside" || draft.timeMode === "before";
  const timeRowMode = showStartTime && showEndTime ? "time-window" : showStartTime || showEndTime ? "time-single" : "time-any";

  return (
    <main className="app-shell">
      <header className="topbar">
        <button className="brand brand-button" onClick={resetHome} aria-label="CenterSeat home" type="button">
          <span className="brand-mark">C</span><span>CENTERSEAT</span>
        </button>
        <div className="topbar-right">
          <button className="topbar-action" onClick={() => setInfoOpen(true)} type="button">How it works</button>
          <button className="topbar-search" onClick={() => openQuery()} type="button">New search <span aria-hidden="true">⌕</span></button>
        </div>
      </header>

      {!hasOutcome ? (
        <section className="cinema-home" aria-label="Choose a movie">
          <div className="cinema-intro">
            <h1>Find the best seat.</h1>
            <p>Search live showtimes and seat maps near you.</p>
            <button className="primary-lobby-action" onClick={() => openQuery()} type="button">Search movies <span aria-hidden="true">→</span></button>
          </div>

          <div className="now-playing">
            <div className="now-playing-head">
              <div><span>IN THEATERS</span><h2>Popular right now</h2></div>
              <div className="rail-actions">
                <a href={trendingSourceURL} target="_blank" rel="noreferrer">Via Rotten Tomatoes ↗</a>
                <button aria-label="Previous movies" onClick={() => movieRail.current?.scrollBy({ left: -520, behavior: "smooth" })} type="button">←</button>
                <button aria-label="Next movies" onClick={() => movieRail.current?.scrollBy({ left: 520, behavior: "smooth" })} type="button">→</button>
              </div>
            </div>
            <div className="movie-rail" ref={movieRail}>
              {trendingState === "loading" ? Array.from({ length: 6 }, (_, index) => <div aria-hidden="true" className="poster-skeleton" key={index}><i /><span><b /><small /><em /></span></div>) : null}
              {trending.map((movie, index) => (
                <button className="movie-card" key={movie.id} onClick={() => openQuery(movie)} type="button">
                  <span className="poster-frame">
                    {/* eslint-disable-next-line @next/next/no-img-element -- provider poster hosts are dynamic. */}
                    <img
                      alt={`${movie.title} poster`}
                      decoding="async"
                      fetchPriority={index < 2 ? "high" : "auto"}
                      height="305"
                      loading={index < 5 ? "eager" : "lazy"}
                      src={movie.poster_url}
                      width="206"
                    />
                    <i>{String(index + 1).padStart(2, "0")}</i>
                  </span>
                  <span className="movie-card-copy">
                    <strong>{movie.title}</strong>
                    <small>{movie.release_text}</small>
                    <span className="movie-scores">
                      {movie.critics_score !== undefined ? <b>{movie.certified_fresh ? "Fresh" : "Critics"} {movie.critics_score}%</b> : null}
                      {movie.audience_score !== undefined ? <b>Audience {movie.audience_score}%</b> : null}
                    </span>
                  </span>
                </button>
              ))}
              {trendingState === "unavailable" ? (
                <div className="trending-unavailable"><span>THE MARQUEE IS BETWEEN SHOWS</span><h3>Popular movies could not be loaded.</h3><p>You can still search the live provider directly.</p><button onClick={() => openQuery()} type="button">Search by title →</button></div>
              ) : null}
            </div>
          </div>
        </section>
      ) : (
        <section className="result-screen" aria-live="polite">
          <div className="result-titlebar">
            <div><span>YOUR BEST AVAILABLE</span><h1>{applied?.movie ?? "Live seat search"}</h1><p>{applied ? `${applied.dateStart} — ${applied.dateEnd} · ${applied.tickets} ${applied.tickets === 1 ? "seat" : "seats"} · ${applied.location} · ${applied.timezone}` : "The live query did not complete."}</p></div>
            <div className="result-title-actions">{applied ? <button onClick={copyShareLink} type="button">Copy link</button> : null}<button onClick={() => setInfoOpen(true)} type="button">What am I seeing?</button><button className="modify-search" onClick={() => openQuery()} type="button">Modify search</button>{copyNotice ? <small role="status">{copyNotice}</small> : null}</div>
          </div>
          {problem ? <ProblemPanel problem={problem} onRetry={() => openQuery()} /> : null}
          {result?.winner && applied ? <LiveResult key={result.query_id} result={result} applied={applied} onRerun={() => void search(undefined, applied)} /> : null}
          {showtimeResult ? <ShowtimeResults result={showtimeResult} timezone={applied?.timezone ?? draft.timezone} /> : null}
          {result && !result.winner ? <div className="no-match-panel"><span>NO EXACT MATCH</span><h2>No seat satisfied every rule.</h2><p>Nothing was fabricated or silently relaxed.</p><button onClick={() => openQuery()} type="button">Adjust search</button></div> : null}
        </section>
      )}

      {queryOpen ? (
        <div className="modal-layer" onMouseDown={() => !searching && setQueryOpen(false)}>
          <div aria-labelledby="query-title" aria-modal="true" className="query-dialog" onMouseDown={(event) => event.stopPropagation()} role="dialog">
            <div className="dialog-head">
              <div><h2 id="query-title">Search</h2></div>
              <button aria-label="Close search" disabled={searching} onClick={() => setQueryOpen(false)} type="button">×</button>
            </div>
            <form className="query-form" onSubmit={search} aria-label="Best seat query">
              {providerState === "discovery" ? <div className="provider-inline"><b>Showtimes only</b><span>This source cannot claim exact seat availability.</span></div> : null}
              {providerState !== "ready" && providerState !== "discovery" ? <div className="provider-inline error"><b>{providerText}</b><span>Start the local live provider before searching.</span></div> : null}

              <div className="essential-grid">
                <label className="field movie-field full-span">
                  <FieldLabel>Movie</FieldLabel>
                  <input
                    aria-activedescendant={activeSuggestion >= 0 ? `${suggestionsID}-${activeSuggestion}` : undefined}
                    aria-autocomplete="list"
                    aria-controls={suggestionsID}
                    aria-expanded={suggestionsOpen}
                    autoComplete="off"
                    autoFocus
                    onBlur={() => window.setTimeout(() => setSuggestionsOpen(false), 120)}
                    onChange={(event) => {
                      const value = event.target.value;
                      setDraft((current) => ({ ...current, movie: value, movieId: undefined }));
                      setSuggestionsOpen(value.trim().length >= 2);
                    }}
                    onFocus={() => setSuggestionsOpen(movieSuggestions.length > 0)}
                    onKeyDown={(event) => {
                      if (event.key === "ArrowDown" && movieSuggestions.length) {
                        event.preventDefault();
                        setSuggestionsOpen(true);
                        setActiveSuggestion((current) => Math.min(current + 1, movieSuggestions.length - 1));
                      } else if (event.key === "ArrowUp" && movieSuggestions.length) {
                        event.preventDefault();
                        setActiveSuggestion((current) => Math.max(current - 1, 0));
                      } else if (event.key === "Enter" && suggestionsOpen && activeSuggestion >= 0) {
                        event.preventDefault();
                        chooseMovie(movieSuggestions[activeSuggestion]);
                      }
                    }}
                    placeholder="Movie title"
                    role="combobox"
                    value={draft.movie}
                    required
                  />
                  {draft.movieId ? <span className="canonical-match">Matched to the provider title ✓</span> : null}
                  {suggestionsOpen && movieSuggestions.length ? <ul className="movie-suggestions" id={suggestionsID} role="listbox">{movieSuggestions.map((suggestion, index) => <li aria-selected={index === activeSuggestion} id={`${suggestionsID}-${index}`} key={suggestion.id} role="option"><button onMouseDown={(event) => event.preventDefault()} onClick={() => chooseMovie(suggestion)} type="button"><span>{suggestion.title}</span><small>{suggestion.year || "Release pending"}</small></button></li>)}</ul> : null}
                </label>

                <div className="search-schedule-row full-span">
                  <label className="field location-field">
                    <FieldLabel>Near</FieldLabel>
                    <span className="input-action-wrap"><input onChange={(event) => { setDraft((current) => ({ ...current, location: event.target.value, latitude: undefined, longitude: undefined })); setLocationProblem(""); }} placeholder={postalLocationSupported ? "ZIP code" : "Location"} value={draft.location} required /><button onClick={useCurrentLocation} type="button">{locating ? "…" : "⌖"}</button></span>
                    <span className="field-hint">{hasCoordinates ? "Current area set" : postalLocationSupported ? "ZIP needs no location permission" : "Use the location button"}</span>
                    {locationProblem ? <span className="location-problem" role="alert">{locationProblem}</span> : null}
                  </label>
                  <label className="field"><FieldLabel>From</FieldLabel><input type="date" value={draft.dateStart} onChange={(event) => update("dateStart", event.target.value)} required /></label>
                  <label className="field"><FieldLabel>Through</FieldLabel><input type="date" min={draft.dateStart} value={draft.dateEnd} onChange={(event) => update("dateEnd", event.target.value)} required /></label>
                </div>

                <div className={`search-time-row full-span ${timeRowMode}`}>
                  <label className="field time-mode-field"><FieldLabel>Time</FieldLabel><select value={draft.timeMode} onChange={(event) => update("timeMode", event.target.value as TimeMode)}>{timeModes.map((mode) => <option key={mode.value} value={mode.value}>{mode.label}</option>)}</select></label>
                  {showStartTime ? <label className="field time-bound-field"><FieldLabel>{draft.timeMode === "after" ? "After" : "From"}</FieldLabel><input type="time" value={draft.startTime} onChange={(event) => update("startTime", event.target.value)} /></label> : null}
                  {showEndTime ? <label className="field time-bound-field"><FieldLabel>{draft.timeMode === "before" ? "Before" : "To"}</FieldLabel><input type="time" value={draft.endTime} onChange={(event) => update("endTime", event.target.value)} /></label> : null}
                </div>
                <label className="field full-span"><FieldLabel>Timezone</FieldLabel><input list="centerseat-timezones" value={draft.timezone} aria-invalid={!isValidTimeZone(draft.timezone)} onChange={(event) => update("timezone", event.target.value)} required /><datalist id="centerseat-timezones">{timezoneOptions.map((timezone) => <option key={timezone} value={timezone} />)}</datalist><span className="field-hint">{isValidTimeZone(draft.timezone) ? "Dates and screening times use this timezone." : "Enter a valid IANA timezone."}</span></label>
                {shareErrors.length ? <div className="dialog-problem" role="alert"><b>Some shared-link values were ignored</b><span>{shareErrors.join(" · ")}</span></div> : null}
                <label className="field"><FieldLabel>Party</FieldLabel><select value={draft.tickets} onChange={(event) => update("tickets", Number(event.target.value))}>{Array.from({ length: 8 }, (_, index) => index + 1).map((count) => <option key={count} value={count}>{count} {count === 1 ? "seat" : "seats"}</option>)}</select></label>
                <label className="field"><FieldLabel>Seat preference</FieldLabel><select value={draft.profile} onChange={(event) => update("profile", event.target.value as SeatProfile)}>{profiles.map((profile) => <option key={profile.value} value={profile.value}>{profile.label} — {profile.hint}</option>)}</select></label>
                {draft.profile === "custom" ? <CustomSeatZonePicker value={draft.customSeatZone} onChange={(zone) => update("customSeatZone", zone)} /> : null}
              </div>

              <details className="query-options">
                <summary><span><b>More settings</b><small>{timeModes.find((mode) => mode.value === draft.timeMode)?.label} · {draft.formats.length} formats · {draft.maxDistance} mi</small></span><i>+</i></summary>
                <div className="option-grid">
                  <fieldset className="format-fieldset full-span"><legend>Formats</legend><div className="chip-row six-chips">{formatOptions.map((format) => <button aria-pressed={draft.formats.includes(format)} className={draft.formats.includes(format) ? "format-chip active" : "format-chip"} key={format} onClick={() => toggleFormat(format)} type="button"><span>{draft.formats.includes(format) ? "✓" : "+"}</span>{format}</button>)}</div></fieldset>
                  <label className="field range-field"><FieldLabel>Distance <em>{draft.maxDistance} mi</em></FieldLabel><input type="range" min="2" max="49" value={draft.maxDistance} onChange={(event) => update("maxDistance", Number(event.target.value))} /></label>
                  <label className="check-field price-ceiling-toggle"><input type="checkbox" checked={draft.limitPrice} onChange={(event) => update("limitPrice", event.target.checked)} /><span><b>Price ceiling</b><small>{draft.limitPrice ? `$${draft.maxPrice} total` : "Any price"}</small></span></label>
                  {draft.limitPrice ? <label className="field range-field"><FieldLabel>Maximum total <em>${draft.maxPrice}</em></FieldLabel><input type="range" min="10" max="200" step="5" value={draft.maxPrice} onChange={(event) => update("maxPrice", Number(event.target.value))} /></label> : null}
                  <label className="field"><FieldLabel>Captions</FieldLabel><select value={draft.captions} onChange={(event) => update("captions", event.target.value)}><option value="any">Any captions</option><option value="open">Open required</option><option value="closed">Closed required</option><option value="none">No captions</option></select></label>
                  <label className="field"><FieldLabel>Skip front</FieldLabel><select value={draft.excludeFirstRows} onChange={(event) => update("excludeFirstRows", Number(event.target.value))}>{[0,1,2,3,4].map((count) => <option key={count} value={count}>{count} rows</option>)}</select></label>
                  <label className="field"><FieldLabel>Wheelchair spaces</FieldLabel><select value={draft.wheelchairSpaces} onChange={(event) => update("wheelchairSpaces", Number(event.target.value))}>{[0,1,2,3,4].map((count) => <option key={count} value={count}>{count}</option>)}</select></label>
                  <label className="field"><FieldLabel>Companion seats</FieldLabel><select value={draft.companionSeats} onChange={(event) => update("companionSeats", Number(event.target.value))}>{[0,1,2,3,4].map((count) => <option key={count} value={count}>{count}</option>)}</select></label>
                  <label className="check-field"><input type="checkbox" checked={draft.recliners} onChange={(event) => update("recliners", event.target.checked)} /><span><b>Recliners</b><small>Check box if required</small></span></label>
                  <label className="check-field"><input type="checkbox" checked={draft.audioDescription} onChange={(event) => update("audioDescription", event.target.checked)} /><span><b>Audio description</b><small>Check box if required</small></span></label>
                  <label className="check-field"><input type="checkbox" checked={draft.allowSplit} onChange={(event) => update("allowSplit", event.target.checked)} /><span><b>Split party</b><small>Allow if needed</small></span></label>
                </div>
              </details>

              {problem && queryOpen ? <div className="dialog-problem" role="alert"><b>{problem.title}</b><span>{problem.detail}</span></div> : null}
              <div className="dialog-submit"><p>Live, read-only inventory. No seat hold is created.</p><button disabled={searching || (providerState !== "ready" && providerState !== "discovery") || !draft.movie.trim() || !draft.location.trim() || !locationReady || draft.formats.length === 0 || !isValidTimeZone(draft.timezone)} type="submit"><span>{searchText}</span><b>{searching ? <i className="button-spinner" /> : "→"}</b></button></div>
            </form>
          </div>
        </div>
      ) : null}

      {infoOpen ? <InfoDialog onClose={() => setInfoOpen(false)} /> : null}
    </main>
  );
}

function ProblemPanel({ problem, onRetry }: { problem: Problem; onRetry: () => void }) {
  return <div className="problem-panel" role="alert"><span>LIVE QUERY STOPPED</span><h2>{problem.title}</h2><p>{problem.detail}</p><button onClick={onRetry} type="button">Review search</button></div>;
}

function InfoDialog({ onClose }: { onClose: () => void }) {
  return <div className="modal-layer info-layer" onMouseDown={onClose}><div aria-labelledby="info-title" aria-modal="true" className="info-dialog" onMouseDown={(event) => event.stopPropagation()} role="dialog"><div className="dialog-head"><div><span>THE SHORT VERSION</span><h2 id="info-title">How CenterSeat chooses.</h2><p>Expensive live checks happen only after easy exclusions.</p></div><button aria-label="Close explanation" onClick={onClose} type="button">×</button></div><ol className="compact-method"><li><b>1</b><span>Discover<small>Every matching date and showtime</small></span></li><li><b>2</b><span>Filter<small>Time, distance, format, price and access</small></span></li><li><b>3</b><span>Compare live seats<small>Keeps checks moving in parallel and expands automatically across every date</small></span></li><li><b>4</b><span>Prove the winner<small>Stops early only when no remaining screening can score higher</small></span></li><li><b>5</b><span>Recheck<small>The winner gets one final live read</small></span></li></ol><p className="info-note">CenterSeat never creates a cart, hold, or purchase. Availability may change until the theater confirms it.</p></div></div>;
}

function ShowtimeResults({ result, timezone }: { result: ShowtimeQueryResponse; timezone: string }) {
  return <div className="showtime-workspace"><div className="showtime-notice"><b>Showtimes only</b><span>This source does not expose exact seats.</span></div><div className="showtime-grid">{result.showtimes.map((showtime) => { const labels = formatShowtime(showtime.starts_at, timezone); return <article className="showtime-card" key={showtime.id}><div className="showtime-card-head"><span>{labels.date}</span><b>{labels.time} {labels.zone}</b></div><h3>{showtime.venue_name}</h3><p>{humanize(showtime.format)} · {showtime.distance_miles.toFixed(1)} mi</p>{showtime.booking_url ? <a href={showtime.booking_url} target="_blank" rel="noreferrer">Open provider ↗</a> : <small>No booking link returned</small>}</article>; })}</div></div>;
}

function QueryDiagnostics({ coverage }: { coverage: SeatQueryResponse["coverage"] }) {
  const failed = coverage.inventories_failed ?? coverage.providers_degraded ?? 0;
  const reasons = Object.entries(coverage.inventory_failure_reasons ?? {}).filter(([, count]) => count > 0).map(([reason, count]) => `${count} ${humanize(reason).toLowerCase()}`).join(" · ");
  return <div className="compact-diagnostics"><span><b>{coverage.inventories_checked}</b> screenings checked</span><span><b>{coverage.inventories_fresh}</b> live maps loaded</span><span><b>{coverage.screenings_unavailable ?? 0}</b> unavailable or no seat match</span><span className={failed ? "has-failure" : ""}><b>{failed}</b> checks interrupted{reasons ? <small>{reasons}</small> : null}</span></div>;
}

function LiveResult({ result, applied, onRerun }: { result: SeatQueryResponse; applied: QueryState; onRerun: () => void }) {
  const winner = result.winner as Recommendation;
  const [active, setActive] = useState<Recommendation>(winner);
  const [loadedRecommendations, setLoadedRecommendations] = useState<Record<number, Recommendation>>({ [winner.rank]: winner });
  const [loadingRank, setLoadingRank] = useState<number | null>(null);
  const [mapProblem, setMapProblem] = useState("");
  const [serverExpired, setServerExpired] = useState(false);
  // 0 until the first client tick keeps render pure; ages read "0s ago" for that first second.
  const [now, setNow] = useState(0);
  const [copyStatus, setCopyStatus] = useState("");
  const [changedSeats, setChangedSeats] = useState<{ before: string[]; after: string[]; recommendation: Recommendation } | null>(null);
  const expired = serverExpired || (now > 0 && now >= new Date(result.refresh_until).getTime());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, []);

  const ageSeconds = (recommendation: Recommendation, at = now) => Math.max(0, Math.floor((at - new Date(recommendation.verified_at).getTime()) / 1_000));
  const pastRefreshWindow = () => isPast(result.refresh_until);
  const ageLabel = (recommendation: Recommendation) => {
    const seconds = ageSeconds(recommendation);
    return seconds < 60 ? `${seconds}s ago` : `${Math.floor(seconds / 60)}m ago`;
  };

  const refreshRecommendation = async (rank: number) => {
    const response = await fetch(`/api/seat-recommendations?query_id=${encodeURIComponent(result.query_id)}&rank=${rank}`, { cache: "no-store" });
    const body = await response.json();
    if (!response.ok) {
      if (response.status === 410) setServerExpired(true);
      throw new Error(`${body.code ? `${body.code}: ` : ""}${body.detail ?? body.title ?? "The selected map could not be refreshed."}`);
    }
    return body as Recommendation;
  };

  const openRecommendation = async (recommendation: Recommendation) => {
    if (expired || pastRefreshWindow()) {
      setServerExpired(true);
      return;
    }
    const loaded = loadedRecommendations[recommendation.rank] ?? recommendation;
    const stale = ageSeconds(loaded, currentTime()) > 30 || !loaded.seat_map;
    if (loaded.rank === active.rank && !stale) return;
    setMapProblem("");
    setLoadingRank(recommendation.rank);
    try {
      const refreshed = stale ? await refreshRecommendation(recommendation.rank) : loaded;
      setLoadedRecommendations((current) => ({ ...current, [refreshed.rank]: refreshed }));
      setActive(refreshed);
    } catch (error) {
      setMapProblem(error instanceof Error ? error.message : "The selected live seat map could not be refreshed.");
    } finally {
      setLoadingRank(null);
    }
  };

  const continueAtProvider = async () => {
    const tab = window.open("about:blank", "_blank");
    if (!tab) {
      setMapProblem("Allow pop-ups to continue to the provider.");
      return;
    }
    tab.opener = null;
    if (expired || pastRefreshWindow()) {
      setServerExpired(true);
      tab.close();
      return;
    }
    setMapProblem("");
    setLoadingRank(active.rank);
    try {
      const refreshed = await refreshRecommendation(active.rank);
      setLoadedRecommendations((current) => ({ ...current, [refreshed.rank]: refreshed }));
      setActive(refreshed);
      const before = active.seats.map((seat) => seat.label);
      const after = refreshed.seats.map((seat) => seat.label);
      if (before.length !== after.length || before.some((label, index) => label !== after[index])) {
        tab.close();
        setChangedSeats({ before, after, recommendation: refreshed });
      } else {
        const destination = refreshed.booking_url || refreshed.showtime.booking_url;
        if (destination) tab.location.href = destination;
        else {
          tab.close();
          setMapProblem("No provider booking URL was returned.");
        }
      }
    } catch (error) {
      tab.close();
      setMapProblem(error instanceof Error ? error.message : "The provider handoff could not be refreshed.");
    } finally {
      setLoadingRank(null);
    }
  };

  const copyHandoff = async (json: boolean) => {
    const shareURL = new URL("/", window.location.origin);
    shareURL.search = searchParamsFromQuery(applied).toString();
    const handoff = buildCheckoutHandoff(active, applied.tickets, applied.timezone, shareURL.toString());
    const text = json ? JSON.stringify(handoff, null, 2) : handoffText(handoff);
    try {
      await navigator.clipboard.writeText(text);
      setCopyStatus(json ? "Agent JSON copied." : "Checkout details copied.");
    } catch {
      window.prompt(json ? "Copy agent JSON" : "Copy checkout details", text);
      setCopyStatus("Copy the text from the dialog.");
    }
  };

  const options = active.seat_options?.length ? active.seat_options : active.seats;
  const labels = options.map((seat) => seat.label);
  const showtime = formatShowtime(active.showtime.starts_at, applied.timezone);
  const availableCount = active.seat_map?.seats.filter((seat) => seat.status === "available").length ?? 0;
  const recommendationChoices = [winner, ...result.alternatives].map((recommendation) => loadedRecommendations[recommendation.rank] ?? recommendation);
  const handoff = buildCheckoutHandoff(active, applied.tickets, applied.timezone);
  const bookingURL = active.booking_url || active.showtime.booking_url;

  return <div className="result-workspace">
    <section className="seat-focus">
      <div className="seat-focus-head"><div><span>{active.rank === 1 ? (result.coverage.range_best_proven ? "BEST ACROSS RANGE" : "BEST FOUND") : `ALTERNATIVE ${active.rank}`} · {ageSeconds(active) <= 30 ? "LIVE CHECKED" : "CHECK AGED"} · {active.profile_match === "closest_fallback" ? "CLOSEST FALLBACK" : "PREFERRED ZONE"}</span><h2>{labels.join(" · ")}</h2><p>{active.showtime.venue_name}{active.showtime.auditorium_name ? ` · ${active.showtime.auditorium_name}` : ""}</p></div><div className="match-score"><b>{active.score}</b><small>/100 match</small></div></div>
      <div className="seat-map-wrap"><SeatMap recommendation={active} /></div>
    </section>

    <aside className="result-rail">
      <div className="result-facts"><span><small>WHEN</small><b>{showtime.time} {showtime.zone}</b><em>{showtime.date}</em></span><span><small>FORMAT</small><b>{humanize(active.showtime.format)}</b><em>{active.showtime.distance_miles.toFixed(1)} mi away</em></span><span><small>LIVE MAP</small><b>{availableCount} open</b><em>checked {ageLabel(active)}</em></span></div>
      {expired ? <div className="rail-warning" role="status">This live result has expired. Refresh is no longer available.</div> : null}
      <button className="booking-link" disabled={loadingRank !== null || expired || !bookingURL} onClick={continueAtProvider} type="button">Continue at provider <span>↗</span></button>
      {expired ? <button className="modify-search" onClick={onRerun} type="button">Re-run search</button> : null}

      <section className="handoff-panel"><h3>Checkout handoff</h3><p><b>{handoff.seats.join(" · ")}</b> · {handoff.ticket_count} {handoff.ticket_count === 1 ? "ticket" : "tickets"}</p><p>{handoff.venue}{handoff.auditorium ? ` · ${handoff.auditorium}` : ""}</p><p>{showtime.date} · {showtime.time} {showtime.zone} · checked {ageLabel(active)}</p><div><button onClick={() => void copyHandoff(false)} type="button">Copy for checkout</button><button onClick={() => void copyHandoff(true)} type="button">Copy agent JSON</button></div>{copyStatus ? <small role="status">{copyStatus}</small> : null}</section>
      {changedSeats ? <div className="rail-warning" role="alert"><b>Seats changed after the live check</b><p>{changedSeats.before.join(" · ")} → {changedSeats.after.join(" · ")}</p><button onClick={() => { const tab = window.open(changedSeats.recommendation.booking_url || changedSeats.recommendation.showtime.booking_url || "", "_blank"); if (tab) tab.opener = null; }} type="button">Continue with these seats</button></div> : null}

      <div className="alternative-list">
        <div className="rail-section-title"><span>COMPARE LIVE OPTIONS</span><small>Return to the best match or open another map</small></div>
        {recommendationChoices.map((recommendation) => {
          const recommendationLabels = (recommendation.seat_options?.length ? recommendation.seat_options : recommendation.seats).map((seat) => seat.label);
          const time = formatShowtime(recommendation.showtime.starts_at, applied.timezone);
          const selected = active.rank === recommendation.rank;
          return <button aria-label={`${recommendation.rank === 1 ? "Best match" : `Option ${recommendation.rank}`}: ${recommendationLabels.join(", ")}`} aria-pressed={selected} className={`${selected ? "selected" : ""}${recommendation.rank === 1 ? " best-choice" : ""}`} disabled={loadingRank !== null || expired} key={recommendation.showtime.id} onClick={() => openRecommendation(recommendation)} type="button"><span><em>{recommendation.rank === 1 ? "BEST MATCH" : `OPTION ${recommendation.rank}`}</em><b>{recommendationLabels.join(" · ")}</b><small>{recommendation.showtime.venue_name}</small></span><span><b>{loadingRank === recommendation.rank ? "…" : recommendation.score}</b><small>{time.time} {time.zone} · checked {ageLabel(recommendation)}</small></span></button>;
        })}
      </div>

      <details className="result-detail"><summary><span>Why this seat</span><i>+</i></summary><p>{active.explanation[0]}</p><ul>{Object.entries(active.score_breakdown).map(([key, value]) => <li key={key}><span>{humanize(key)}</span><b>{Math.round(value)}</b></li>)}</ul></details>
      <details className="result-detail"><summary><span>Query details</span><i>+</i></summary><QueryDiagnostics coverage={result.coverage} /><p>{result.coverage.dates_compared} of {result.coverage.dates_with_screenings} dates compared · {result.coverage.range_best_proven ? "No remaining screening could beat this result" : "Range winner not fully proven"} · {result.coverage.screenings_discovered} showtimes discovered · discovery {result.coverage.discovery_ms} ms · live maps {result.coverage.inventory_ms} ms · final check {result.coverage.verification_ms} ms</p></details>
      {mapProblem ? <div className="rail-warning" role="alert">{mapProblem}</div> : null}
      {result.warnings?.map((warning) => <div className="rail-warning" key={warning}>{warning}</div>)}
      <small className="read-only-note">Read only · no order or seat hold created</small>
    </aside>
  </div>;
}
