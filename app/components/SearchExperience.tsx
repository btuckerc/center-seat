"use client";

import { FormEvent, useEffect, useState } from "react";
import {
  createDefaultQuery,
  type QueryState,
  type Recommendation,
  type SeatProfile,
  type SeatQueryResponse,
  type Showtime,
  type ShowtimeQueryResponse,
  type TimeMode,
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
type Problem = { title: string; detail: string; status?: number };

function FieldLabel({ number, children }: { number?: string; children: React.ReactNode }) {
  return (
    <span className="field-label">
      {number ? <b>{number}</b> : null}
      {children}
    </span>
  );
}

const dateLabel = (value: string) =>
  new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(`${value}T12:00:00`));

const showtimeLabels = (recommendation: Recommendation) => ({
  date: new Intl.DateTimeFormat(undefined, { weekday: "short", month: "short", day: "numeric" }).format(
    new Date(recommendation.showtime.starts_at),
  ),
  time: new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(
    new Date(recommendation.showtime.starts_at),
  ),
});

const screeningLabels = (showtime: Showtime) => ({
  date: new Intl.DateTimeFormat(undefined, { weekday: "short", month: "short", day: "numeric" }).format(
    new Date(showtime.starts_at),
  ),
  time: new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(
    new Date(showtime.starts_at),
  ),
});

const humanize = (value: string) =>
  value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());

export function SearchExperience() {
  const [draft, setDraft] = useState<QueryState>(() => createDefaultQuery());
  const [applied, setApplied] = useState<QueryState | null>(null);
  const [result, setResult] = useState<SeatQueryResponse | null>(null);
  const [showtimeResult, setShowtimeResult] = useState<ShowtimeQueryResponse | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [providerState, setProviderState] = useState<ProviderState>("checking");
  const [searching, setSearching] = useState(false);
  const [locating, setLocating] = useState(false);
  const [locationProblem, setLocationProblem] = useState("");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let active = true;
    fetch("/api/providers", { cache: "no-store" })
      .then(async (response) => {
        const payload = (await response.json()) as {
          providers?: Array<{ configured: boolean; status: string; kind: string }>;
        };
        if (!active) return;
        const statuses = payload.providers ?? [];
        const discoveryReady = statuses.some((provider) => provider.kind === "discovery" && provider.configured && provider.status === "healthy");
        const inventoryReady = statuses.some((provider) => provider.kind === "inventory" && provider.configured && provider.status === "healthy");
        if (response.ok && discoveryReady && inventoryReady) {
          setProviderState("ready");
        } else if (response.ok && discoveryReady) {
          setProviderState("discovery");
        } else {
          setProviderState("unconfigured");
        }
      })
      .catch(() => active && setProviderState("unavailable"));
    return () => { active = false; };
  }, []);

  const update = <K extends keyof QueryState>(key: K, value: QueryState[K]) => {
    setDraft((current) => ({ ...current, [key]: value }));
  };

  const toggleFormat = (format: string) => {
    setDraft((current) => ({
      ...current,
      formats: current.formats.includes(format)
        ? current.formats.filter((item) => item !== format)
        : [...current.formats, format],
    }));
  };

  const useCurrentLocation = () => {
    if (!navigator.geolocation) {
      setLocationProblem("This browser does not expose precise location.");
      return;
    }
    setLocating(true);
    setLocationProblem("");
    navigator.geolocation.getCurrentPosition(
      (position) => {
        setDraft((current) => ({
          ...current,
          location: "Current location",
          latitude: position.coords.latitude,
          longitude: position.coords.longitude,
        }));
        setLocationProblem("");
        setLocating(false);
      },
      () => {
        setLocationProblem("Location permission was not granted. Live nearby-showtime sources require coordinates.");
        setLocating(false);
      },
      { enableHighAccuracy: true, timeout: 10_000, maximumAge: 300_000 },
    );
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
    setResult(null);
    setShowtimeResult(null);
    const payload = {
      movie_query: draft.movie.trim(),
      location: {
        query: draft.location.trim(),
        ...(draft.latitude !== undefined ? { latitude: draft.latitude } : {}),
        ...(draft.longitude !== undefined ? { longitude: draft.longitude } : {}),
        radius_miles: draft.maxDistance,
      },
      dates: { start: draft.dateStart, end: draft.dateEnd },
      time: {
        mode: draft.timeMode,
        start: draft.startTime,
        end: draft.endTime,
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      },
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
      candidate_limit: 12,
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
        setProblem({
          title: body.title ?? "Live query failed",
          detail: body.detail ?? "The configured provider did not return a usable response.",
          status: response.status,
        });
      } else {
        setApplied({ ...draft });
        if (providerState === "ready") setResult(body as SeatQueryResponse);
        else setShowtimeResult(body as ShowtimeQueryResponse);
        requestAnimationFrame(() =>
          document.getElementById("results")?.scrollIntoView({ behavior: "smooth", block: "start" }),
        );
      }
    } catch {
      setProblem({
        title: "Live query unavailable",
        detail: "CenterSeat could not reach the configured live source. No substitute result was returned.",
      });
    } finally {
      setSearching(false);
    }
  };

  const copyQuery = async () => {
    if (!applied) return;
    await navigator.clipboard.writeText(JSON.stringify(applied, null, 2));
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1400);
  };

  const providerText = {
    checking: "Checking live providers",
    ready: "Showtimes + seats connected",
    discovery: "Live showtimes connected",
    unconfigured: "Provider setup required",
    unavailable: "Provider health unavailable",
  }[providerState];

  const searchText = searching
    ? "Querying live providers…"
    : providerState === "ready"
      ? draft.latitude === undefined || draft.longitude === undefined
        ? "Use precise location to continue"
        : "Find live seats"
      : providerState === "discovery"
        ? draft.latitude === undefined || draft.longitude === undefined
          ? "Use precise location to continue"
          : "Find live showtimes"
        : "Connect live providers to search";

  return (
    <main>
      <header className="topbar">
        <a className="brand" href="#top" aria-label="CenterSeat home">
          <span className="brand-mark">C</span><span>CENTERSEAT</span>
        </a>
        <div className="topbar-right">
          <span className={`network-status ${providerState}`}><i />{providerText}</span>
          <a className="text-link" href="#method">How it works</a>
        </div>
      </header>

      <section className="hero" id="top">
        <div className="hero-copy">
          <div className="eyebrow"><span>01</span> Seat intelligence, not another showtime list</div>
          <h1>Find the one<br />worth booking.</h1>
          <p>
            Query real showtimes across a date range. When live seat inventory is available,
            get one exact recommendation based on the theater&apos;s real geometry.
          </p>
          <div className="hero-proof">
            <div><strong>LIVE</strong><span>configured sources only</span></div>
            <div><strong>FINAL</strong><span>winner reverified</span></div>
            <div><strong>0</strong><span>seat holds created</span></div>
          </div>
        </div>

        <form className="query-card" onSubmit={search} aria-label="Best seat query">
          <div className="query-card-head">
            <div><span className="kicker">NEW QUERY</span><h2>What should we optimize?</h2></div>
            <span className="read-only-badge">READ ONLY</span>
          </div>

          {providerState === "discovery" ? (
            <div className="provider-warning" role="status">
              <b>{providerText}</b>
              <span>
                Open Cinema discovery is connected. Searches return real indie and repertory screenings, but make no seat-availability claim until a separate inventory source is connected. <a href="https://opencinema.app/developers" target="_blank" rel="noreferrer">Open Cinema data ↗</a>
              </span>
            </div>
          ) : providerState !== "ready" ? (
            <div className="provider-warning" role="status">
              <b>{providerText}</b>
              <span>CenterSeat is refusing to show fabricated results. Configure a supported live provider, or start the personal read-only provider locally.</span>
            </div>
          ) : null}

          <div className="field-stack">
            <label className="field wide-field">
              <FieldLabel number="1">Movie</FieldLabel>
              <input value={draft.movie} onChange={(event) => update("movie", event.target.value)} placeholder="Movie title" required />
            </label>

            <div className="field-row location-row">
              <label className="field">
                <FieldLabel number="2">Near</FieldLabel>
                <input
                  value={draft.location}
                  onChange={(event) => {
                    setDraft((current) => ({ ...current, location: event.target.value, latitude: undefined, longitude: undefined }));
                    setLocationProblem("Use precise location after changing this label.");
                  }}
                  placeholder="Location label"
                  required
                />
              </label>
              <button className="location-button" onClick={useCurrentLocation} type="button">
                {locating ? "Locating…" : draft.latitude !== undefined ? "Precise location set ✓" : "Use precise location · required"}
              </button>
              {locationProblem ? <span className="location-problem" role="alert">{locationProblem}</span> : null}
            </div>

            <div className="field-row date-range-row">
              <label className="field">
                <FieldLabel number="3">Date range starts</FieldLabel>
                <input type="date" value={draft.dateStart} onChange={(event) => update("dateStart", event.target.value)} required />
              </label>
              <span className="range-connector">through</span>
              <label className="field">
                <FieldLabel>Range ends</FieldLabel>
                <input type="date" min={draft.dateStart} value={draft.dateEnd} onChange={(event) => update("dateEnd", event.target.value)} required />
              </label>
            </div>

            <div className="field-row time-row">
              <label className="field">
                <FieldLabel number="4">Time rule</FieldLabel>
                <select value={draft.timeMode} onChange={(event) => update("timeMode", event.target.value as TimeMode)}>
                  {timeModes.map((mode) => <option key={mode.value} value={mode.value}>{mode.label}</option>)}
                </select>
              </label>
              <label className="field compact-field">
                <FieldLabel>From</FieldLabel>
                <input type="time" value={draft.startTime} disabled={draft.timeMode === "any" || draft.timeMode === "before"} onChange={(event) => update("startTime", event.target.value)} />
              </label>
              <label className="field compact-field">
                <FieldLabel>To</FieldLabel>
                <input type="time" value={draft.endTime} disabled={draft.timeMode === "any" || draft.timeMode === "after"} onChange={(event) => update("endTime", event.target.value)} />
              </label>
            </div>

            <div className="field-row one-two">
              <label className="field">
                <FieldLabel number="5">Tickets</FieldLabel>
                <select value={draft.tickets} onChange={(event) => update("tickets", Number(event.target.value))}>
                  {Array.from({ length: 8 }, (_, index) => index + 1).map((count) => <option key={count} value={count}>{count} {count === 1 ? "seat" : "seats"}</option>)}
                </select>
              </label>
              <label className="field">
                <FieldLabel number="6">Seat preference</FieldLabel>
                <select value={draft.profile} onChange={(event) => update("profile", event.target.value as SeatProfile)}>
                  {profiles.map((profile) => <option key={profile.value} value={profile.value}>{profile.label} — {profile.hint}</option>)}
                </select>
              </label>
            </div>

            <fieldset className="format-fieldset">
              <legend><span className="number-dot">7</span>Formats to consider</legend>
              <div className="chip-row six-chips">
                {formatOptions.map((format) => (
                  <button aria-pressed={draft.formats.includes(format)} className={draft.formats.includes(format) ? "format-chip active" : "format-chip"} key={format} onClick={() => toggleFormat(format)} type="button">
                    <span>{draft.formats.includes(format) ? "✓" : "+"}</span>{format}
                  </button>
                ))}
              </div>
            </fieldset>
          </div>

          <details className="advanced">
            <summary><span><b>Advanced constraints</b><small>Price, distance, access, amenities, party rules</small></span><span className="summary-arrow">+</span></summary>
            <div className="advanced-grid">
              <label className="field range-field">
                <FieldLabel>Maximum distance <em>{draft.maxDistance} mi</em></FieldLabel>
                <input type="range" min="2" max="49" value={draft.maxDistance} onChange={(event) => update("maxDistance", Number(event.target.value))} />
              </label>
              <label className="check-field price-toggle">
                <input type="checkbox" checked={draft.limitPrice} onChange={(event) => update("limitPrice", event.target.checked)} />
                <span><b>Set price ceiling</b><small>{draft.limitPrice ? `$${draft.maxPrice} total` : "Do not filter unknown prices"}</small></span>
              </label>
              <label className="field range-field">
                <FieldLabel>Maximum total <em>${draft.maxPrice}</em></FieldLabel>
                <input type="range" min="10" max="200" step="5" value={draft.maxPrice} disabled={!draft.limitPrice} onChange={(event) => update("maxPrice", Number(event.target.value))} />
              </label>
              <label className="field">
                <FieldLabel>Captions</FieldLabel>
                <select value={draft.captions} onChange={(event) => update("captions", event.target.value)}>
                  <option value="any">Any captions</option><option value="open">Open captions required</option><option value="closed">Closed captions required</option><option value="none">No captions</option>
                </select>
              </label>
              <label className="field"><FieldLabel>Skip first rows</FieldLabel><select value={draft.excludeFirstRows} onChange={(event) => update("excludeFirstRows", Number(event.target.value))}>{[0,1,2,3,4].map((count) => <option key={count} value={count}>{count} rows</option>)}</select></label>
              <label className="field"><FieldLabel>Wheelchair spaces</FieldLabel><select value={draft.wheelchairSpaces} onChange={(event) => update("wheelchairSpaces", Number(event.target.value))}>{[0,1,2,3,4].map((count) => <option key={count} value={count}>{count}</option>)}</select></label>
              <label className="field"><FieldLabel>Companion seats</FieldLabel><select value={draft.companionSeats} onChange={(event) => update("companionSeats", Number(event.target.value))}>{[0,1,2,3,4].map((count) => <option key={count} value={count}>{count}</option>)}</select></label>
              <label className="check-field"><input type="checkbox" checked={draft.recliners} onChange={(event) => update("recliners", event.target.checked)} /><span><b>Recliners required</b><small>Exclude standard seating</small></span></label>
              <label className="check-field"><input type="checkbox" checked={draft.audioDescription} onChange={(event) => update("audioDescription", event.target.checked)} /><span><b>Audio description</b><small>Require supported showings</small></span></label>
              <label className="check-field"><input type="checkbox" checked={draft.allowSplit} onChange={(event) => update("allowSplit", event.target.checked)} /><span><b>Allow a split party</b><small>Only when no block fits</small></span></label>
            </div>
          </details>

          <button className="search-button" disabled={searching || (providerState !== "ready" && providerState !== "discovery") || !draft.movie.trim() || !draft.location.trim() || draft.latitude === undefined || draft.longitude === undefined || draft.formats.length === 0} type="submit">
            <span>{searchText}</span><b>{searching ? <i className="button-spinner" /> : "→"}</b>
          </button>
          <p className="query-note">No demo fallback. No holds. Discovery-only results are labeled and never presented as seat availability.</p>
        </form>
      </section>

      <section className="results-section" id="results">
        <div className="section-heading">
          <div>
            <span className="eyebrow dark"><span>02</span> Live recommendation</span>
            <h2>{applied?.movie || "Awaiting a live query"}</h2>
            <p>{applied ? `${dateLabel(applied.dateStart)} through ${dateLabel(applied.dateEnd)} · ${applied.tickets} ${applied.tickets === 1 ? "seat" : "seats"} near ${applied.location}` : "Results appear only after configured live providers respond."}</p>
          </div>
          {applied ? <button className="copy-button" onClick={copyQuery} type="button">{copied ? "Copied query" : "Copy reproducible query"}</button> : null}
        </div>

        {problem ? (
          <div className="connection-state error-state" role="alert"><span>LIVE QUERY STOPPED</span><h3>{problem.title}</h3><p>{problem.detail}</p>{problem.status ? <small>HTTP {problem.status} · no substitute result returned</small> : null}</div>
        ) : null}

        {!problem && providerState !== "ready" && providerState !== "discovery" ? (
          <div className="connection-state"><span>PRODUCTION SAFETY</span><h3>Live discovery is not connected yet.</h3><p>Synthetic results remain disabled. Configure Open Cinema for discovery, or run the personal Fandango provider locally for exact seat maps.</p><div className="connection-requirements"><b>Required</b><span>Configured live source</span><span>Precise location</span><span>Server-side configuration</span></div></div>
        ) : null}

        {!problem && providerState === "discovery" && !showtimeResult ? (
          <div className="connection-state discovery-state"><span>DISCOVERY READY</span><h3>Real showtimes are connected.</h3><p>Run a query to browse the configured source&apos;s coverage. Exact-seat ranking stays disabled until live inventory is connected.</p></div>
        ) : null}

        {!problem && providerState === "ready" && !result ? (
          <div className="connection-state ready-state"><span>READY</span><h3>Licensed providers are connected.</h3><p>Run a query to compare live inventory across the complete date range.</p></div>
        ) : null}

        {result?.winner ? <LiveResult result={result} /> : null}

        {showtimeResult ? <ShowtimeResults result={showtimeResult} /> : null}

        {result && !result.winner ? (
          <div className="empty-result"><span>NO EXACT MATCH</span><h3>No live seat satisfied every constraint.</h3><p>CenterSeat did not silently relax the date, time, format, accessibility, geometry, price, or distance rules.</p></div>
        ) : null}
      </section>

      <section className="method-section" id="method">
        <div className="method-intro"><span className="eyebrow"><span>03</span> Built for repeatability</span><h2>Fast because it asks<br />expensive questions last.</h2><p>Discovery is cached broadly. Live inventory is requested only for screenings that survive the hard constraints. The exact winner is refreshed one final time.</p></div>
        <ol className="method-steps">
          <li><span>01</span><div><b>Discover every date</b><p>Licensed showtimes are normalized across the full requested date range.</p></div><small>1–31 days</small></li>
          <li><span>02</span><div><b>Prune cheaply</b><p>Time, distance, price, format, accessibility, and amenity constraints reduce fan-out.</p></div><small>Local operation</small></li>
          <li><span>03</span><div><b>Check in parallel</b><p>Only top candidates receive bounded live-inventory requests.</p></div><small>5–12 maps</small></li>
          <li><span>04</span><div><b>Rank actual geometry</b><p>The same coordinate model drives both scoring and the map you see.</p></div><small>One authority</small></li>
          <li><span>05</span><div><b>Verify the winner</b><p>A final read-only refresh prevents stale recommendations without creating holds.</p></div><small>Live read</small></li>
        </ol>
      </section>

      <footer><a className="brand footer-brand" href="#top"><span className="brand-mark">C</span><span>CENTERSEAT</span></a><p>Exact seats. Explicit constraints. No fabricated inventory.</p><span>QUERY RELEASE · 2026</span></footer>
    </main>
  );
}

function ShowtimeResults({ result }: { result: ShowtimeQueryResponse }) {
  return (
    <>
      <div className="coverage-strip" aria-label="Showtime query coverage">
        <span><b>{result.coverage.screenings_discovered}</b> discovered</span><i>→</i>
        <span><b>{result.showtimes.length}</b> matched constraints</span><i>→</i>
        <span className="discovery-only"><b>0</b> seat maps claimed</span><small>Open Cinema Project</small>
      </div>
      {result.showtimes.length ? (
        <div className="showtime-grid">
          {result.showtimes.map((showtime) => {
            const labels = screeningLabels(showtime);
            return (
              <article className="showtime-card" key={showtime.id}>
                <div className="showtime-card-head"><span>LIVE SHOWTIME · SEATS PENDING</span><b>{labels.time}</b></div>
                <h3>{showtime.movie_title}</h3>
                <p>{showtime.venue_name}</p>
                <div className="showtime-card-facts"><span>{labels.date}</span><span>{humanize(showtime.format)}</span><span>{showtime.distance_miles.toFixed(1)} mi</span></div>
                {showtime.booking_url ? <a href={showtime.booking_url} target="_blank" rel="noreferrer">Open provider booking page <span>↗</span></a> : <small>No checkout link supplied</small>}
              </article>
            );
          })}
        </div>
      ) : (
        <div className="empty-result"><span>NO SHOWTIMES FOUND</span><h3>Open Cinema returned no matching screening.</h3><p>Its current coverage focuses on independent, repertory, and arthouse theaters; Charlotte returned no nearby records during provider validation.</p></div>
      )}
      {result.warnings?.length ? <div className="result-warnings">{result.warnings.map((warning) => <span key={warning}>{warning}</span>)}</div> : null}
    </>
  );
}

function LiveResult({ result }: { result: SeatQueryResponse }) {
  const winner = result.winner as Recommendation;
  const labels = winner.seats.map((seat) => seat.label);
  const showtime = showtimeLabels(winner);
  const availableCount = winner.seat_map?.seats.filter((seat) => seat.status === "available").length ?? 0;
  return (
    <>
      <div className="coverage-strip" aria-label="Query coverage">
        <span><b>{result.coverage.screenings_discovered}</b> discovered</span><i>→</i>
        <span><b>{result.coverage.screenings_pruned}</b> matched constraints</span><i>→</i>
        <span><b>{result.coverage.inventories_checked}</b> live maps checked</span><i>→</i>
        <span className="verified"><b>1</b> winner reverified</span><small>{result.coverage.elapsed_ms} ms</small>
      </div>
      <article className="winner-card">
        <div className="winner-main">
          <div className="winner-head"><div><span className="winner-label">BEST AVAILABLE · LIVE</span><h3>{labels.join(" + ")}</h3><p>{winner.showtime.venue_name}{winner.showtime.auditorium_name ? ` · ${winner.showtime.auditorium_name}` : ""}</p></div><div className="score-ring" aria-label={`${winner.score} percent match`}><strong>{winner.score}</strong><small>/100</small><span>MATCH</span></div></div>
          <div className="showtime-facts">
            <div><span>SHOWTIME</span><b>{showtime.time}</b><small>{showtime.date}</small></div>
            <div><span>FORMAT</span><b>{humanize(winner.showtime.format)}</b><small>{winner.showtime.amenities?.map(humanize).join(" · ") || "Provider supplied"}</small></div>
            <div><span>DISTANCE</span><b>{winner.showtime.distance_miles.toFixed(1)} mi</b><small>From query location</small></div>
            <div><span>LIVE MAP</span><b>{availableCount} open</b><small>Verified {new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(0, "second")}</small></div>
          </div>
          <SeatMap recommendation={winner} />
        </div>
        <aside className="winner-aside">
          <div className="freshness"><span><i />LIVE INVENTORY</span><b>Verified {new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit", second: "2-digit" }).format(new Date(winner.verified_at))}</b><small>No order or seat hold was created</small></div>
          <div className="why-block"><span className="aside-kicker">WHY THIS WINS</span><h4>{winner.explanation[0]}</h4><ul>{Object.entries(winner.score_breakdown).map(([key, value]) => <li key={key}><b>{Math.round(value)}</b><span>{humanize(key)}<small>{key === "horizontal_alignment" ? `Target x ${(winner.seat_map?.target.x ?? .5) * 100}%` : "Provider geometry"}</small></span></li>)}</ul></div>
          {winner.booking_url ? <a className="booking-link" href={winner.booking_url} target="_blank" rel="noreferrer">Open provider booking page <span>↗</span></a> : <div className="booking-unavailable">No provider booking link was returned. CenterSeat will not invent one.</div>}
          <p className="booking-note">Query-only release. Availability can change until the theater confirms a purchase.</p>
        </aside>
      </article>
      {result.warnings?.length ? <div className="result-warnings">{result.warnings.map((warning) => <span key={warning}>{warning}</span>)}</div> : null}
      {result.alternatives.length ? <><div className="alternatives-heading"><div><span className="aside-kicker">LIVE-CHECKED ALTERNATIVES</span><h3>Trade time, distance, or format.</h3></div><span>No fabricated availability</span></div><div className="alternative-grid">{result.alternatives.map((recommendation) => { const labels = recommendation.seats.map((seat) => seat.label); const time = showtimeLabels(recommendation); return <article className="alternative-card" key={recommendation.showtime.id}><div className="alt-top"><span>0{recommendation.rank}</span><b>{recommendation.score}<small>/100</small></b></div><h4>{labels.join(" + ")}</h4><p>{recommendation.showtime.venue_name}</p><div className="alt-facts"><span>{time.time}</span><span>{humanize(recommendation.showtime.format)}</span><span>{recommendation.showtime.distance_miles.toFixed(1)} mi</span></div><div className="alt-bottom"><span>{time.date}</span><span>Verified live</span></div></article>; })}</div></> : null}
    </>
  );
}
