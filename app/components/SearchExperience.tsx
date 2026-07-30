"use client";

import { FormEvent, useEffect, useState } from "react";
import {
  createDefaultQuery,
  type QueryState,
  type Recommendation,
  type SeatProfile,
  type SeatQueryResponse,
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

type ProviderState = "checking" | "ready" | "unconfigured" | "unavailable";
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

const humanize = (value: string) =>
  value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());

export function SearchExperience() {
  const [draft, setDraft] = useState<QueryState>(() => createDefaultQuery());
  const [applied, setApplied] = useState<QueryState | null>(null);
  const [result, setResult] = useState<SeatQueryResponse | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [providerState, setProviderState] = useState<ProviderState>("checking");
  const [searching, setSearching] = useState(false);
  const [locating, setLocating] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let active = true;
    fetch("/api/providers", { cache: "no-store" })
      .then(async (response) => {
        const payload = (await response.json()) as {
          providers?: Array<{ configured: boolean; status: string }>;
        };
        if (!active) return;
        const statuses = payload.providers ?? [];
        if (
          response.ok &&
          statuses.length >= 2 &&
          statuses.every((provider) => provider.configured && provider.status !== "disabled")
        ) {
          setProviderState("ready");
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
    if (!navigator.geolocation) return;
    setLocating(true);
    navigator.geolocation.getCurrentPosition(
      (position) => {
        setDraft((current) => ({
          ...current,
          location: "Current location",
          latitude: position.coords.latitude,
          longitude: position.coords.longitude,
        }));
        setLocating(false);
      },
      () => setLocating(false),
      { enableHighAccuracy: true, timeout: 10_000, maximumAge: 300_000 },
    );
  };

  const search = async (event: FormEvent) => {
    event.preventDefault();
    if (providerState !== "ready" || searching) return;
    if (draft.dateEnd < draft.dateStart) {
      setProblem({ title: "Invalid date range", detail: "The end date must be on or after the start date." });
      return;
    }
    setSearching(true);
    setProblem(null);
    setResult(null);
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
      const response = await fetch("/api/seat-queries", {
        method: "POST",
        headers: { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
        body: JSON.stringify(payload),
      });
      const body = await response.json();
      if (!response.ok) {
        setProblem({
          title: body.title ?? "Live query failed",
          detail: body.detail ?? "The licensed provider did not return a usable response.",
          status: response.status,
        });
      } else {
        setApplied({ ...draft });
        setResult(body as SeatQueryResponse);
        requestAnimationFrame(() =>
          document.getElementById("results")?.scrollIntoView({ behavior: "smooth", block: "start" }),
        );
      }
    } catch {
      setProblem({
        title: "Live query unavailable",
        detail: "CenterSeat could not reach the licensed inventory service. No substitute result was returned.",
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
    ready: "Licensed providers connected",
    unconfigured: "Provider setup required",
    unavailable: "Provider health unavailable",
  }[providerState];

  const searchText = searching
    ? "Querying licensed providers…"
    : providerState === "ready"
      ? "Find live seats"
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
            Query licensed showtimes across a date range, compare live seat inventory,
            and get one exact recommendation based on the theater&apos;s real geometry.
          </p>
          <div className="hero-proof">
            <div><strong>LIVE</strong><span>licensed inventory only</span></div>
            <div><strong>FINAL</strong><span>winner reverified</span></div>
            <div><strong>0</strong><span>seat holds created</span></div>
          </div>
        </div>

        <form className="query-card" onSubmit={search} aria-label="Best seat query">
          <div className="query-card-head">
            <div><span className="kicker">NEW QUERY</span><h2>What should we optimize?</h2></div>
            <span className="read-only-badge">READ ONLY</span>
          </div>

          {providerState !== "ready" ? (
            <div className="provider-warning" role="status">
              <b>{providerText}</b>
              <span>
                CenterSeat is refusing to show fabricated inventory. A licensed Vista tenant and the production query service must be connected before search is enabled.
              </span>
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
                  onChange={(event) => setDraft((current) => ({ ...current, location: event.target.value, latitude: undefined, longitude: undefined }))}
                  placeholder="City, ZIP, theater, or address"
                  required
                />
              </label>
              <button className="location-button" onClick={useCurrentLocation} type="button">
                {locating ? "Locating…" : "Use precise location"}
              </button>
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
                <input type="range" min="2" max="100" value={draft.maxDistance} onChange={(event) => update("maxDistance", Number(event.target.value))} />
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

          <button className="search-button" disabled={searching || providerState !== "ready" || !draft.movie.trim() || !draft.location.trim() || draft.formats.length === 0} type="submit">
            <span>{searchText}</span><b>{searching ? <i className="button-spinner" /> : "→"}</b>
          </button>
          <p className="query-note">No demo fallback. No holds. The winning live map is rechecked before it is returned.</p>
        </form>
      </section>

      <section className="results-section" id="results">
        <div className="section-heading">
          <div>
            <span className="eyebrow dark"><span>02</span> Live recommendation</span>
            <h2>{applied?.movie || "Awaiting a live query"}</h2>
            <p>{applied ? `${dateLabel(applied.dateStart)} through ${dateLabel(applied.dateEnd)} · ${applied.tickets} ${applied.tickets === 1 ? "seat" : "seats"} near ${applied.location}` : "Results appear only after licensed providers respond."}</p>
          </div>
          {applied ? <button className="copy-button" onClick={copyQuery} type="button">{copied ? "Copied query" : "Copy reproducible query"}</button> : null}
        </div>

        {problem ? (
          <div className="connection-state error-state" role="alert"><span>LIVE QUERY STOPPED</span><h3>{problem.title}</h3><p>{problem.detail}</p>{problem.status ? <small>HTTP {problem.status} · no substitute result returned</small> : null}</div>
        ) : null}

        {!problem && providerState !== "ready" ? (
          <div className="connection-state"><span>PRODUCTION SAFETY</span><h3>Live inventory is not connected yet.</h3><p>The previous synthetic results have been removed. Search will remain disabled until the licensed provider service is configured and healthy.</p><div className="connection-requirements"><b>Required</b><span>Vista OCAPI tenant</span><span>GAS credentials</span><span>Production API deployment</span></div></div>
        ) : null}

        {!problem && providerState === "ready" && !result ? (
          <div className="connection-state ready-state"><span>READY</span><h3>Licensed providers are connected.</h3><p>Run a query to compare live inventory across the complete date range.</p></div>
        ) : null}

        {result?.winner ? <LiveResult result={result} /> : null}

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
          {winner.booking_url ? <a className="booking-link" href={winner.booking_url} target="_blank" rel="noreferrer">Open licensed booking page <span>↗</span></a> : <div className="booking-unavailable">No provider booking link was returned. CenterSeat will not invent one.</div>}
          <p className="booking-note">Query-only release. Availability can change until the theater confirms a purchase.</p>
        </aside>
      </article>
      {result.warnings?.length ? <div className="result-warnings">{result.warnings.map((warning) => <span key={warning}>{warning}</span>)}</div> : null}
      {result.alternatives.length ? <><div className="alternatives-heading"><div><span className="aside-kicker">LIVE-CHECKED ALTERNATIVES</span><h3>Trade time, distance, or format.</h3></div><span>No fabricated availability</span></div><div className="alternative-grid">{result.alternatives.map((recommendation) => { const labels = recommendation.seats.map((seat) => seat.label); const time = showtimeLabels(recommendation); return <article className="alternative-card" key={recommendation.showtime.id}><div className="alt-top"><span>0{recommendation.rank}</span><b>{recommendation.score}<small>/100</small></b></div><h4>{labels.join(" + ")}</h4><p>{recommendation.showtime.venue_name}</p><div className="alt-facts"><span>{time.time}</span><span>{humanize(recommendation.showtime.format)}</span><span>{recommendation.showtime.distance_miles.toFixed(1)} mi</span></div><div className="alt-bottom"><span>{time.date}</span><span>Verified live</span></div></article>; })}</div></> : null}
    </>
  );
}
