"use client";

import { FormEvent, useEffect, useId, useRef, useState } from "react";
import {
  createDefaultQuery,
  type MovieSuggestion,
  type ProviderStatus,
  type QueryState,
  type Recommendation,
  type SeatProfile,
  type SeatQueryResponse,
  type Showtime,
  type ShowtimeQueryResponse,
  type TimeMode,
  type TrendingMovie,
} from "../lib/api";
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
];

type ProviderState = "checking" | "ready" | "discovery" | "unconfigured" | "unavailable";
type TrendingState = "loading" | "ready" | "unavailable";
type Problem = { title: string; detail: string; status?: number };
type SavedPreferences = Pick<QueryState,
  "location" | "tickets" | "profile" | "formats" | "maxDistance" | "candidateLimit" |
  "recliners" | "captions" | "audioDescription" | "wheelchairSpaces" | "companionSeats" |
  "excludeFirstRows" | "allowSplit" | "limitPrice" | "maxPrice"
>;

const preferencesKey = "centerseat.preferences.v1";
const postalCodePattern = /^\s*\d{5}(?:-\d{4})?\s*$/;

function FieldLabel({ children }: { children: React.ReactNode }) {
  return <span className="field-label">{children}</span>;
}

const showtimeLabels = (recommendation: Recommendation) => ({
  date: new Intl.DateTimeFormat(undefined, { weekday: "short", month: "short", day: "numeric" }).format(new Date(recommendation.showtime.starts_at)),
  time: new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(new Date(recommendation.showtime.starts_at)),
});

const screeningLabels = (showtime: Showtime) => ({
  date: new Intl.DateTimeFormat(undefined, { weekday: "short", month: "short", day: "numeric" }).format(new Date(showtime.starts_at)),
  time: new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(new Date(showtime.starts_at)),
});

const humanize = (value: string) => value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
const normalizeTitle = (value: string) => value.toLocaleLowerCase().replace(/[^a-z0-9]+/g, "").trim();

export function SearchExperience() {
  const [draft, setDraft] = useState<QueryState>(() => createDefaultQuery());
  const [applied, setApplied] = useState<QueryState | null>(null);
  const [result, setResult] = useState<SeatQueryResponse | null>(null);
  const [showtimeResult, setShowtimeResult] = useState<ShowtimeQueryResponse | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [providerState, setProviderState] = useState<ProviderState>("checking");
  const [providerDegraded, setProviderDegraded] = useState(false);
  const [postalLocationSupported, setPostalLocationSupported] = useState(false);
  const [searching, setSearching] = useState(false);
  const [locating, setLocating] = useState(false);
  const [locationProblem, setLocationProblem] = useState("");
  const [queryOpen, setQueryOpen] = useState(false);
  const [infoOpen, setInfoOpen] = useState(false);
  const [trending, setTrending] = useState<TrendingMovie[]>([]);
  const [trendingState, setTrendingState] = useState<TrendingState>("loading");
  const [trendingSourceURL, setTrendingSourceURL] = useState("https://www.rottentomatoes.com/browse/movies_in_theaters/sort:popular");
  const [movieSuggestions, setMovieSuggestions] = useState<MovieSuggestion[]>([]);
  const [suggestionsOpen, setSuggestionsOpen] = useState(false);
  const [activeSuggestion, setActiveSuggestion] = useState(-1);
  const movieRail = useRef<HTMLDivElement>(null);
  const suggestionsID = useId();

  useEffect(() => {
    const timer = window.setTimeout(() => {
      try {
        const saved = JSON.parse(window.localStorage.getItem(preferencesKey) ?? "null") as Partial<SavedPreferences> | null;
        if (!saved) return;
        setDraft((current) => ({ ...current, ...saved, movie: "", movieId: undefined, latitude: undefined, longitude: undefined }));
      } catch {
        window.localStorage.removeItem(preferencesKey);
      }
    }, 0);
    return () => window.clearTimeout(timer);
  }, []);

  useEffect(() => {
    let active = true;
    fetch("/api/trending-movies", { cache: "no-store" })
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
  }, []);

  useEffect(() => {
    let active = true;
    fetch("/api/providers", { cache: "no-store" })
      .then(async (response) => {
        const payload = (await response.json()) as { providers?: ProviderStatus[] };
        if (!active) return;
        const statuses = payload.providers ?? [];
        const discoveryConfigured = statuses.some((provider) => provider.kind === "discovery" && provider.configured && provider.status !== "disabled");
        const inventoryConfigured = statuses.some((provider) => provider.kind === "inventory" && provider.configured && provider.status !== "disabled");
        const configuredStatuses = statuses.filter((provider) => provider.configured && provider.status !== "disabled");
        setProviderDegraded(configuredStatuses.some((provider) => provider.status !== "healthy"));
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
      tickets: draft.tickets,
      profile: draft.profile,
      formats: draft.formats,
      maxDistance: draft.maxDistance,
      candidateLimit: draft.candidateLimit,
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

  const search = async (event: FormEvent) => {
    event.preventDefault();
    if ((providerState !== "ready" && providerState !== "discovery") || searching) return;
    if (draft.dateEnd < draft.dateStart) {
      setProblem({ title: "Invalid date range", detail: "The end date must be on or after the start date." });
      return;
    }
    setSearching(true);
    setProblem(null);
    const payload = {
      movie_query: draft.movie.trim(),
      ...(draft.movieId ? { movie_id: draft.movieId } : {}),
      location: {
        query: draft.location.trim(),
        ...(draft.latitude !== undefined ? { latitude: draft.latitude } : {}),
        ...(draft.longitude !== undefined ? { longitude: draft.longitude } : {}),
        radius_miles: draft.maxDistance,
      },
      dates: { start: draft.dateStart, end: draft.dateEnd },
      time: { mode: draft.timeMode, start: draft.startTime, end: draft.endTime, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone },
      ticket_count: draft.tickets,
      seat_profile: draft.profile,
      formats: draft.formats.map((format) => format.toLowerCase()),
      captions: draft.captions,
      audio_description: draft.audioDescription,
      wheelchair_spaces: draft.wheelchairSpaces,
      companion_seats: draft.companionSeats,
      amenities_required: draft.recliners ? ["recliner"] : [],
      max_distance_miles: draft.maxDistance,
      ...(draft.limitPrice ? { max_total_price: draft.maxPrice } : {}),
      allow_unknown_price: !draft.limitPrice,
      exclude_first_rows: draft.excludeFirstRows,
      allow_split_party: draft.allowSplit,
      minimum_geometry_confidence: "row_geometry",
      candidate_limit: draft.candidateLimit,
    };
    try {
      const endpoint = providerState === "ready" ? "/api/seat-queries" : "/api/showtime-queries";
      const response = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
        body: JSON.stringify(payload),
      });
      const body = await response.json();
      if (!response.ok) {
        setProblem({ title: body.title ?? "Live query failed", detail: body.detail ?? "The live source did not return a usable response.", status: response.status });
      } else {
        setApplied({ ...draft });
        if (providerState === "ready") {
          setResult(body as SeatQueryResponse);
          setShowtimeResult(null);
        } else {
          setShowtimeResult(body as ShowtimeQueryResponse);
          setResult(null);
        }
        persistPreferences();
        setQueryOpen(false);
      }
    } catch {
      setProblem({ title: "Live query unavailable", detail: "CenterSeat could not reach the configured source. No substitute result was returned." });
    } finally {
      setSearching(false);
    }
  };

  const providerText = {
    checking: "Checking live sources",
    ready: "Live seats connected",
    discovery: "Showtimes connected",
    unconfigured: "Source required",
    unavailable: "Source unavailable",
  }[providerState];
  const effectiveProviderText = providerDegraded && (providerState === "ready" || providerState === "discovery") ? "Live source warming up" : providerText;
  const hasOutcome = Boolean(result || showtimeResult || problem);
  const searchText = searching ? "Checking live seats…" : providerState === "ready" ? "Find my best seat" : providerState === "discovery" ? "Find showtimes" : "Live source required";

  return (
    <main className="app-shell">
      <header className="topbar">
        <button className="brand brand-button" onClick={resetHome} aria-label="CenterSeat home" type="button">
          <span className="brand-mark">C</span><span>CENTERSEAT</span>
        </button>
        <div className="topbar-right">
          <span className={`network-status ${providerState}${providerDegraded ? " degraded" : ""}`}><i />{effectiveProviderText}</span>
          <button className="topbar-action" onClick={() => setInfoOpen(true)} type="button">How it works</button>
          <button className="topbar-search" onClick={() => openQuery()} type="button">New search <span>⌕</span></button>
        </div>
      </header>

      {!hasOutcome ? (
        <section className="cinema-home" aria-label="Choose a movie">
          <div className="cinema-intro">
            <span className="lobby-kicker"><i /> NOW SHOWING</span>
            <h1>The best seat<br /><em>still open.</em></h1>
            <p>Choose a movie, tell us where, and go straight to the strongest live seat map.</p>
            <button className="primary-lobby-action" onClick={() => openQuery()} type="button">Search any movie <span>→</span></button>
            <div className="lobby-assurance"><span>Live inventory</span><span>Read only</span><span>Final recheck</span></div>
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
              {trendingState === "loading" ? Array.from({ length: 6 }, (_, index) => <div className="poster-skeleton" key={index}><i /><span /></div>) : null}
              {trending.map((movie, index) => (
                <button className="movie-card" key={movie.id} onClick={() => openQuery(movie)} type="button">
                  <span className="poster-frame">
                    {/* eslint-disable-next-line @next/next/no-img-element -- provider poster hosts are dynamic. */}
                    <img alt={`${movie.title} poster`} loading={index < 3 ? "eager" : "lazy"} src={movie.poster_url} />
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
            <div><span>YOUR BEST AVAILABLE</span><h1>{applied?.movie ?? "Live seat search"}</h1><p>{applied ? `${applied.dateStart} — ${applied.dateEnd} · ${applied.tickets} ${applied.tickets === 1 ? "seat" : "seats"} · ${applied.location}` : "The live query did not complete."}</p></div>
            <div className="result-title-actions"><button onClick={() => setInfoOpen(true)} type="button">What am I seeing?</button><button className="modify-search" onClick={() => openQuery()} type="button">Modify search</button></div>
          </div>
          {problem ? <ProblemPanel problem={problem} onRetry={() => openQuery()} /> : null}
          {result?.winner ? <LiveResult key={result.query_id} result={result} /> : null}
          {showtimeResult ? <ShowtimeResults result={showtimeResult} /> : null}
          {result && !result.winner ? <div className="no-match-panel"><span>NO EXACT MATCH</span><h2>No seat satisfied every rule.</h2><p>Nothing was fabricated or silently relaxed.</p><button onClick={() => openQuery()} type="button">Adjust search</button></div> : null}
        </section>
      )}

      {queryOpen ? (
        <div className="modal-layer" onMouseDown={() => !searching && setQueryOpen(false)}>
          <div aria-labelledby="query-title" aria-modal="true" className="query-dialog" onMouseDown={(event) => event.stopPropagation()} role="dialog">
            <div className="dialog-head">
              <div><span>NEW SEAT SEARCH</span><h2 id="query-title">Set the essentials.</h2><p>The rest already has sensible defaults.</p></div>
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

                <label className="field location-field">
                  <FieldLabel>Near</FieldLabel>
                  <span className="input-action-wrap"><input onChange={(event) => { setDraft((current) => ({ ...current, location: event.target.value, latitude: undefined, longitude: undefined })); setLocationProblem(""); }} placeholder={postalLocationSupported ? "ZIP code" : "Location"} value={draft.location} required /><button onClick={useCurrentLocation} type="button">{locating ? "…" : "⌖"}</button></span>
                  <span className="field-hint">{hasCoordinates ? "Current area set" : postalLocationSupported ? "ZIP needs no location permission" : "Use the location button"}</span>
                  {locationProblem ? <span className="location-problem" role="alert">{locationProblem}</span> : null}
                </label>

                <div className="date-pair">
                  <label className="field"><FieldLabel>From</FieldLabel><input type="date" value={draft.dateStart} onChange={(event) => update("dateStart", event.target.value)} required /></label>
                  <label className="field"><FieldLabel>Through</FieldLabel><input type="date" min={draft.dateStart} value={draft.dateEnd} onChange={(event) => update("dateEnd", event.target.value)} required /></label>
                </div>

                <label className="field"><FieldLabel>Party</FieldLabel><select value={draft.tickets} onChange={(event) => update("tickets", Number(event.target.value))}>{Array.from({ length: 8 }, (_, index) => index + 1).map((count) => <option key={count} value={count}>{count} {count === 1 ? "seat" : "seats"}</option>)}</select></label>
                <label className="field"><FieldLabel>Best means</FieldLabel><select value={draft.profile} onChange={(event) => update("profile", event.target.value as SeatProfile)}>{profiles.map((profile) => <option key={profile.value} value={profile.value}>{profile.label} — {profile.hint}</option>)}</select></label>
              </div>

              <details className="query-options">
                <summary><span><b>Fine-tune the search</b><small>{timeModes.find((mode) => mode.value === draft.timeMode)?.label} · {draft.formats.length} formats · {draft.maxDistance} mi</small></span><i>+</i></summary>
                <div className="option-grid">
                  <label className="field"><FieldLabel>Time rule</FieldLabel><select value={draft.timeMode} onChange={(event) => update("timeMode", event.target.value as TimeMode)}>{timeModes.map((mode) => <option key={mode.value} value={mode.value}>{mode.label}</option>)}</select></label>
                  <label className="field"><FieldLabel>From</FieldLabel><input type="time" value={draft.startTime} disabled={draft.timeMode === "any" || draft.timeMode === "before"} onChange={(event) => update("startTime", event.target.value)} /></label>
                  <label className="field"><FieldLabel>To</FieldLabel><input type="time" value={draft.endTime} disabled={draft.timeMode === "any" || draft.timeMode === "after"} onChange={(event) => update("endTime", event.target.value)} /></label>
                  <fieldset className="format-fieldset full-span"><legend>Formats</legend><div className="chip-row six-chips">{formatOptions.map((format) => <button aria-pressed={draft.formats.includes(format)} className={draft.formats.includes(format) ? "format-chip active" : "format-chip"} key={format} onClick={() => toggleFormat(format)} type="button"><span>{draft.formats.includes(format) ? "✓" : "+"}</span>{format}</button>)}</div></fieldset>
                  <label className="field range-field"><FieldLabel>Distance <em>{draft.maxDistance} mi</em></FieldLabel><input type="range" min="2" max="49" value={draft.maxDistance} onChange={(event) => update("maxDistance", Number(event.target.value))} /></label>
                  <label className="field"><FieldLabel>Search depth</FieldLabel><select value={draft.candidateLimit} onChange={(event) => update("candidateLimit", Number(event.target.value))}><option value="6">Fast · 6 maps</option><option value="12">Balanced · 12 maps</option><option value="18">Thorough · 18 maps</option></select></label>
                  <label className="check-field"><input type="checkbox" checked={draft.limitPrice} onChange={(event) => update("limitPrice", event.target.checked)} /><span><b>Price ceiling</b><small>{draft.limitPrice ? `$${draft.maxPrice} total` : "Any price"}</small></span></label>
                  {draft.limitPrice ? <label className="field range-field"><FieldLabel>Maximum total <em>${draft.maxPrice}</em></FieldLabel><input type="range" min="10" max="200" step="5" value={draft.maxPrice} onChange={(event) => update("maxPrice", Number(event.target.value))} /></label> : null}
                  <label className="field"><FieldLabel>Captions</FieldLabel><select value={draft.captions} onChange={(event) => update("captions", event.target.value)}><option value="any">Any captions</option><option value="open">Open required</option><option value="closed">Closed required</option><option value="none">No captions</option></select></label>
                  <label className="field"><FieldLabel>Skip front</FieldLabel><select value={draft.excludeFirstRows} onChange={(event) => update("excludeFirstRows", Number(event.target.value))}>{[0,1,2,3,4].map((count) => <option key={count} value={count}>{count} rows</option>)}</select></label>
                  <label className="field"><FieldLabel>Wheelchair spaces</FieldLabel><select value={draft.wheelchairSpaces} onChange={(event) => update("wheelchairSpaces", Number(event.target.value))}>{[0,1,2,3,4].map((count) => <option key={count} value={count}>{count}</option>)}</select></label>
                  <label className="field"><FieldLabel>Companion seats</FieldLabel><select value={draft.companionSeats} onChange={(event) => update("companionSeats", Number(event.target.value))}>{[0,1,2,3,4].map((count) => <option key={count} value={count}>{count}</option>)}</select></label>
                  <label className="check-field"><input type="checkbox" checked={draft.recliners} onChange={(event) => update("recliners", event.target.checked)} /><span><b>Recliners</b><small>Required</small></span></label>
                  <label className="check-field"><input type="checkbox" checked={draft.audioDescription} onChange={(event) => update("audioDescription", event.target.checked)} /><span><b>Audio description</b><small>Required</small></span></label>
                  <label className="check-field"><input type="checkbox" checked={draft.allowSplit} onChange={(event) => update("allowSplit", event.target.checked)} /><span><b>Split party</b><small>Allow if needed</small></span></label>
                </div>
              </details>

              {problem && queryOpen ? <div className="dialog-problem" role="alert"><b>{problem.title}</b><span>{problem.detail}</span></div> : null}
              <div className="dialog-submit"><p>Live, read-only inventory. No seat hold is created.</p><button disabled={searching || (providerState !== "ready" && providerState !== "discovery") || !draft.movie.trim() || !draft.location.trim() || !locationReady || draft.formats.length === 0} type="submit"><span>{searchText}</span><b>{searching ? <i className="button-spinner" /> : "→"}</b></button></div>
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
  return <div className="modal-layer info-layer" onMouseDown={onClose}><div aria-labelledby="info-title" aria-modal="true" className="info-dialog" onMouseDown={(event) => event.stopPropagation()} role="dialog"><div className="dialog-head"><div><span>THE SHORT VERSION</span><h2 id="info-title">How CenterSeat chooses.</h2><p>Expensive live checks happen only after easy exclusions.</p></div><button aria-label="Close explanation" onClick={onClose} type="button">×</button></div><ol className="compact-method"><li><b>1</b><span>Discover<small>Every matching date and showtime</small></span></li><li><b>2</b><span>Filter<small>Time, distance, format, price and access</small></span></li><li><b>3</b><span>Read maps<small>Only the strongest 6–18 candidates</small></span></li><li><b>4</b><span>Rank geometry<small>Real coordinates, not seat-label guesses</small></span></li><li><b>5</b><span>Recheck<small>The winner gets one final live read</small></span></li></ol><p className="info-note">CenterSeat never creates a cart, hold, or purchase. Availability may change until the theater confirms it.</p></div></div>;
}

function ShowtimeResults({ result }: { result: ShowtimeQueryResponse }) {
  return <div className="showtime-workspace"><div className="showtime-notice"><b>Showtimes only</b><span>This source does not expose exact seats.</span></div><div className="showtime-grid">{result.showtimes.map((showtime) => { const labels = screeningLabels(showtime); return <article className="showtime-card" key={showtime.id}><div className="showtime-card-head"><span>{labels.date}</span><b>{labels.time}</b></div><h3>{showtime.venue_name}</h3><p>{humanize(showtime.format)} · {showtime.distance_miles.toFixed(1)} mi</p>{showtime.booking_url ? <a href={showtime.booking_url} target="_blank" rel="noreferrer">Open provider ↗</a> : <small>No booking link returned</small>}</article>; })}</div></div>;
}

function QueryDiagnostics({ coverage }: { coverage: SeatQueryResponse["coverage"] }) {
  const failed = coverage.inventories_failed ?? coverage.providers_degraded ?? 0;
  const reasons = Object.entries(coverage.inventory_failure_reasons ?? {}).filter(([, count]) => count > 0).map(([reason, count]) => `${count} ${humanize(reason).toLowerCase()}`).join(" · ");
  return <div className="compact-diagnostics"><span><b>{coverage.inventories_fresh}/{coverage.inventories_checked}</b> maps loaded</span><span><b>{coverage.screenings_unavailable ?? 0}</b> no eligible block</span><span><b>{coverage.screenings_price_rejected ?? 0}</b> price filtered</span><span className={failed ? "has-failure" : ""}><b>{failed}</b> failed{reasons ? <small>{reasons}</small> : null}</span></div>;
}

function LiveResult({ result }: { result: SeatQueryResponse }) {
  const winner = result.winner as Recommendation;
  const [active, setActive] = useState<Recommendation>(winner);
  const [loadingRank, setLoadingRank] = useState<number | null>(null);
  const [mapProblem, setMapProblem] = useState("");

  const openRecommendation = async (recommendation: Recommendation) => {
    if (recommendation.rank === active.rank && recommendation.seat_map) return;
    setMapProblem("");
    if (recommendation.seat_map) {
      setActive(recommendation);
      return;
    }
    setLoadingRank(recommendation.rank);
    try {
      const response = await fetch(`/api/seat-recommendations?query_id=${encodeURIComponent(result.query_id)}&rank=${recommendation.rank}`, { cache: "no-store" });
      const body = await response.json();
      if (!response.ok) throw new Error(body.detail ?? "The selected map could not be refreshed.");
      setActive(body as Recommendation);
    } catch (error) {
      setMapProblem(error instanceof Error ? error.message : "The selected map could not be refreshed.");
    } finally {
      setLoadingRank(null);
    }
  };

  const options = active.seat_options?.length ? active.seat_options : active.seats;
  const labels = options.map((seat) => seat.label);
  const showtime = showtimeLabels(active);
  const availableCount = active.seat_map?.seats.filter((seat) => seat.status === "available").length ?? 0;
  const activeIsFinal = active.rank !== 1 || result.coverage.winner_verified;

  return <div className="result-workspace">
    <section className="seat-focus">
      <div className="seat-focus-head"><div><span>{active.rank === 1 ? "BEST AVAILABLE" : `ALTERNATIVE ${active.rank}`} · {activeIsFinal ? "FINAL CHECK" : "INITIAL CHECK"}</span><h2>{labels.join(" · ")}</h2><p>{active.showtime.venue_name}{active.showtime.auditorium_name ? ` · ${active.showtime.auditorium_name}` : ""}</p></div><div className="match-score"><b>{active.score}</b><small>/100 match</small></div></div>
      <div className="seat-map-wrap"><SeatMap recommendation={active} /></div>
    </section>

    <aside className="result-rail">
      <div className="result-facts"><span><small>WHEN</small><b>{showtime.time}</b><em>{showtime.date}</em></span><span><small>FORMAT</small><b>{humanize(active.showtime.format)}</b><em>{active.showtime.distance_miles.toFixed(1)} mi away</em></span><span><small>LIVE MAP</small><b>{availableCount} open</b><em>{activeIsFinal ? "Reverified" : "Refresh incomplete"}</em></span></div>
      {active.booking_url ? <a className="booking-link" href={active.booking_url} target="_blank" rel="noreferrer">Continue at provider <span>↗</span></a> : <div className="booking-unavailable">No provider booking link returned.</div>}

      {result.alternatives.length ? <div className="alternative-list"><div className="rail-section-title"><span>OTHER STRONG OPTIONS</span><small>Tap to open its live map</small></div>{result.alternatives.map((recommendation) => { const recommendationLabels = (recommendation.seat_options?.length ? recommendation.seat_options : recommendation.seats).map((seat) => seat.label); const time = showtimeLabels(recommendation); return <button aria-pressed={active.rank === recommendation.rank} className={active.rank === recommendation.rank ? "selected" : ""} disabled={loadingRank !== null} key={recommendation.showtime.id} onClick={() => openRecommendation(recommendation)} type="button"><span><b>{recommendationLabels.join(" · ")}</b><small>{recommendation.showtime.venue_name}</small></span><span><b>{loadingRank === recommendation.rank ? "…" : recommendation.score}</b><small>{time.time}</small></span></button>; })}</div> : null}

      <details className="result-detail"><summary><span>Why this seat</span><i>+</i></summary><p>{active.explanation[0]}</p><ul>{Object.entries(active.score_breakdown).map(([key, value]) => <li key={key}><span>{humanize(key)}</span><b>{Math.round(value)}</b></li>)}</ul></details>
      <details className="result-detail"><summary><span>Query details</span><i>+</i></summary><QueryDiagnostics coverage={result.coverage} /><p>{result.coverage.screenings_discovered} showtimes discovered · {result.coverage.elapsed_ms} ms</p></details>
      {mapProblem ? <div className="rail-warning" role="alert">{mapProblem}</div> : null}
      {result.warnings?.map((warning) => <div className="rail-warning" key={warning}>{warning}</div>)}
      <small className="read-only-note">Read only · no order or seat hold created</small>
    </aside>
  </div>;
}
