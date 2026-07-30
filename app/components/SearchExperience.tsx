"use client";

import { FormEvent, useMemo, useState } from "react";
import {
  defaultQuery,
  QueryState,
  runDemoQuery,
  seatLabels,
  type SeatProfile,
  type TimeMode,
} from "../lib/demo";
import { SeatMap } from "./SeatMap";

const formatOptions = ["Standard", "Dolby", "IMAX", "XD"];
const timeModes: { value: TimeMode; label: string }[] = [
  { value: "inside", label: "Inside this window" },
  { value: "outside", label: "Outside this window" },
  { value: "before", label: "Before this time" },
  { value: "after", label: "After this time" },
  { value: "any", label: "Any time" },
];
const profiles: { value: SeatProfile; label: string; hint: string }[] = [
  { value: "balanced", label: "Best overall", hint: "Center + ideal depth" },
  { value: "dead_center", label: "Dead center", hint: "Exact geometric middle" },
  { value: "two_thirds_back", label: "2/3 back", hint: "Classic viewing zone" },
  { value: "aisle", label: "Near an aisle", hint: "Easy in and out" },
  { value: "front", label: "Closer", hint: "Immersive front section" },
  { value: "back", label: "Back rows", hint: "More distance" },
];

function FieldLabel({ number, children }: { number?: string; children: React.ReactNode }) {
  return (
    <span className="field-label">
      {number ? <b>{number}</b> : null}
      {children}
    </span>
  );
}

export function SearchExperience() {
  const [draft, setDraft] = useState<QueryState>(defaultQuery);
  const [applied, setApplied] = useState<QueryState>(defaultQuery);
  const [phase, setPhase] = useState<"ready" | "discovering" | "checking" | "verifying">("ready");
  const [copied, setCopied] = useState(false);
  const result = useMemo(() => runDemoQuery(applied), [applied]);
  const searching = phase !== "ready";

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

  const search = async (event: FormEvent) => {
    event.preventDefault();
    if (searching) return;
    setPhase("discovering");
    await new Promise((resolve) => window.setTimeout(resolve, 260));
    setPhase("checking");
    await new Promise((resolve) => window.setTimeout(resolve, 320));
    setPhase("verifying");
    await new Promise((resolve) => window.setTimeout(resolve, 220));
    setApplied({ ...draft });
    setPhase("ready");
    document.getElementById("results")?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const copyQuery = async () => {
    const payload = {
      movie_query: applied.movie,
      location: { query: applied.location, radius_miles: applied.maxDistance },
      dates: { start: applied.date, end: applied.date },
      time: {
        mode: applied.timeMode,
        start: applied.startTime,
        end: applied.endTime,
        timezone: "America/New_York",
      },
      ticket_count: applied.tickets,
      seat_profile: applied.profile,
      formats: applied.formats.map((format) => format.toLowerCase()),
      max_total_price: applied.maxPrice,
    };
    await navigator.clipboard.writeText(JSON.stringify(payload, null, 2));
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1400);
  };

  const statusText = {
    ready: `Search ${draft.formats.length || "all"} formats`,
    discovering: "Finding every screening…",
    checking: "Checking live seat maps…",
    verifying: "Verifying the winner…",
  }[phase];

  const timeSummary =
    applied.timeMode === "any"
      ? "at any time"
      : applied.timeMode === "outside"
        ? `outside ${applied.startTime}–${applied.endTime}`
        : applied.timeMode === "before"
          ? `before ${applied.endTime}`
          : applied.timeMode === "after"
            ? `after ${applied.startTime}`
            : `between ${applied.startTime}–${applied.endTime}`;

  return (
    <main>
      <header className="topbar">
        <a className="brand" href="#top" aria-label="CenterSeat home">
          <span className="brand-mark">C</span>
          <span>CENTERSEAT</span>
        </a>
        <div className="topbar-right">
          <span className="network-status"><i />Demo network · healthy</span>
          <a className="text-link" href="#method">How it works</a>
        </div>
      </header>

      <section className="hero" id="top">
        <div className="hero-copy">
          <div className="eyebrow"><span>01</span> Seat intelligence, not another showtime list</div>
          <h1>Find the one<br />worth booking.</h1>
          <p>
            CenterSeat checks every matching screening, opens the best live seat maps,
            and returns one exact seat—with the reasoning to trust it.
          </p>
          <div className="hero-proof">
            <div><strong>34</strong><span>screenings discovered</span></div>
            <div><strong>684<small>ms</small></strong><span>last full query</span></div>
            <div><strong>8<small>s</small></strong><span>inventory freshness</span></div>
          </div>
        </div>

        <form className="query-card" onSubmit={search} aria-label="Best seat query">
          <div className="query-card-head">
            <div>
              <span className="kicker">NEW QUERY</span>
              <h2>What should we optimize?</h2>
            </div>
            <span className="read-only-badge">READ ONLY</span>
          </div>

          <div className="field-stack">
            <label className="field wide-field">
              <FieldLabel number="1">Movie</FieldLabel>
              <input
                value={draft.movie}
                onChange={(event) => update("movie", event.target.value)}
                placeholder="Title, sequel, or reissue"
                required
              />
            </label>

            <div className="field-row two-one">
              <label className="field">
                <FieldLabel number="2">Near</FieldLabel>
                <input
                  value={draft.location}
                  onChange={(event) => update("location", event.target.value)}
                  placeholder="City, ZIP, or address"
                  required
                />
              </label>
              <label className="field">
                <FieldLabel>Date</FieldLabel>
                <input
                  type="date"
                  value={draft.date}
                  onChange={(event) => update("date", event.target.value)}
                />
              </label>
            </div>

            <div className="field-row time-row">
              <label className="field">
                <FieldLabel number="3">Time rule</FieldLabel>
                <select
                  value={draft.timeMode}
                  onChange={(event) => update("timeMode", event.target.value as TimeMode)}
                >
                  {timeModes.map((mode) => (
                    <option key={mode.value} value={mode.value}>{mode.label}</option>
                  ))}
                </select>
              </label>
              <label className="field compact-field">
                <FieldLabel>From</FieldLabel>
                <input
                  type="time"
                  value={draft.startTime}
                  disabled={draft.timeMode === "any" || draft.timeMode === "before"}
                  onChange={(event) => update("startTime", event.target.value)}
                />
              </label>
              <label className="field compact-field">
                <FieldLabel>To</FieldLabel>
                <input
                  type="time"
                  value={draft.endTime}
                  disabled={draft.timeMode === "any" || draft.timeMode === "after"}
                  onChange={(event) => update("endTime", event.target.value)}
                />
              </label>
            </div>

            <div className="field-row one-two">
              <label className="field">
                <FieldLabel number="4">Tickets</FieldLabel>
                <select
                  value={draft.tickets}
                  onChange={(event) => update("tickets", Number(event.target.value))}
                >
                  {Array.from({ length: 8 }, (_, index) => index + 1).map((count) => (
                    <option key={count} value={count}>{count} {count === 1 ? "seat" : "seats"}</option>
                  ))}
                </select>
              </label>
              <label className="field">
                <FieldLabel number="5">Seat preference</FieldLabel>
                <select
                  value={draft.profile}
                  onChange={(event) => update("profile", event.target.value as SeatProfile)}
                >
                  {profiles.map((profile) => (
                    <option key={profile.value} value={profile.value}>
                      {profile.label} — {profile.hint}
                    </option>
                  ))}
                </select>
              </label>
            </div>

            <fieldset className="format-fieldset">
              <legend><span className="number-dot">6</span>Formats to consider</legend>
              <div className="chip-row">
                {formatOptions.map((format) => (
                  <button
                    aria-pressed={draft.formats.includes(format)}
                    className={draft.formats.includes(format) ? "format-chip active" : "format-chip"}
                    key={format}
                    onClick={() => toggleFormat(format)}
                    type="button"
                  >
                    <span>{draft.formats.includes(format) ? "✓" : "+"}</span>{format}
                  </button>
                ))}
              </div>
            </fieldset>
          </div>

          <details className="advanced">
            <summary>
              <span><b>Advanced constraints</b><small>Price, distance, access, amenities, party rules</small></span>
              <span className="summary-arrow">+</span>
            </summary>
            <div className="advanced-grid">
              <label className="field range-field">
                <FieldLabel>Maximum distance <em>{draft.maxDistance} mi</em></FieldLabel>
                <input
                  type="range" min="2" max="50" value={draft.maxDistance}
                  onChange={(event) => update("maxDistance", Number(event.target.value))}
                />
              </label>
              <label className="field range-field">
                <FieldLabel>Maximum total <em>${draft.maxPrice}</em></FieldLabel>
                <input
                  type="range" min="10" max="160" step="5" value={draft.maxPrice}
                  onChange={(event) => update("maxPrice", Number(event.target.value))}
                />
              </label>
              <label className="field">
                <FieldLabel>Captions</FieldLabel>
                <select value={draft.captions} onChange={(event) => update("captions", event.target.value)}>
                  <option>Any captions</option>
                  <option>Open captions required</option>
                  <option>Closed captions required</option>
                  <option>No preference</option>
                </select>
              </label>
              <label className="field">
                <FieldLabel>Skip first rows</FieldLabel>
                <select
                  value={draft.excludeFirstRows}
                  onChange={(event) => update("excludeFirstRows", Number(event.target.value))}
                >
                  {[0, 1, 2, 3, 4].map((count) => <option key={count} value={count}>{count} rows</option>)}
                </select>
              </label>
              <label className="field">
                <FieldLabel>Wheelchair spaces</FieldLabel>
                <select
                  value={draft.wheelchairSpaces}
                  onChange={(event) => update("wheelchairSpaces", Number(event.target.value))}
                >
                  {[0, 1, 2, 3, 4].map((count) => <option key={count} value={count}>{count}</option>)}
                </select>
              </label>
              <label className="field">
                <FieldLabel>Companion seats</FieldLabel>
                <select
                  value={draft.companionSeats}
                  onChange={(event) => update("companionSeats", Number(event.target.value))}
                >
                  {[0, 1, 2, 3, 4].map((count) => <option key={count} value={count}>{count}</option>)}
                </select>
              </label>
              <label className="check-field">
                <input type="checkbox" checked={draft.recliners} onChange={(event) => update("recliners", event.target.checked)} />
                <span><b>Recliners required</b><small>Exclude standard seating</small></span>
              </label>
              <label className="check-field">
                <input type="checkbox" checked={draft.audioDescription} onChange={(event) => update("audioDescription", event.target.checked)} />
                <span><b>Audio description</b><small>Require supported showings</small></span>
              </label>
              <label className="check-field">
                <input type="checkbox" checked={draft.allowSplit} onChange={(event) => update("allowSplit", event.target.checked)} />
                <span><b>Allow a split party</b><small>Only when no block fits</small></span>
              </label>
            </div>
          </details>

          <button className="search-button" disabled={searching || !draft.movie || !draft.location || draft.formats.length === 0} type="submit">
            <span>{statusText}</span>
            <b>{searching ? <i className="button-spinner" /> : "→"}</b>
          </button>
          <p className="query-note">No holds. No phantom availability. The winning map is rechecked before you see it.</p>
        </form>
      </section>

      <section className="results-section" id="results">
        <div className="section-heading">
          <div>
            <span className="eyebrow dark"><span>02</span> Live recommendation</span>
            <h2>{applied.movie}</h2>
            <p>{applied.tickets} adjacent {applied.tickets === 1 ? "seat" : "seats"} near {applied.location}, {timeSummary}</p>
          </div>
          <button className="copy-button" onClick={copyQuery} type="button">
            {copied ? "Copied query" : "Copy reproducible query"}
          </button>
        </div>

        {result.winner ? (
          <>
            <div className="coverage-strip" aria-label="Query coverage">
              <span><b>{result.coverage.discovered}</b> discovered</span>
              <i>→</i>
              <span><b>{result.coverage.matched}</b> matched constraints</span>
              <i>→</i>
              <span><b>{result.coverage.checked}</b> live maps checked</span>
              <i>→</i>
              <span className="verified"><b>1</b> winner reverified</span>
              <small>{result.coverage.elapsed} ms</small>
            </div>

            <article className="winner-card">
              <div className="winner-main">
                <div className="winner-head">
                  <div>
                    <span className="winner-label">BEST AVAILABLE</span>
                    <h3>{seatLabels(result.winner, applied.tickets).join(" + ")}</h3>
                    <p>{result.winner.venue} · {result.winner.room}</p>
                  </div>
                  <div className="score-ring" aria-label={`${result.winner.score} percent match`}>
                    <strong>{result.winner.score}</strong><small>/100</small><span>MATCH</span>
                  </div>
                </div>

                <div className="showtime-facts">
                  <div><span>SHOWTIME</span><b>{result.winner.time}</b><small>Fri, Jul 31</small></div>
                  <div><span>FORMAT</span><b>{result.winner.format}</b><small>{result.winner.amenity}</small></div>
                  <div><span>DISTANCE</span><b>{result.winner.distance} mi</b><small>≈ 12 min away</small></div>
                  <div><span>TOTAL</span><b>${(result.winner.ticketPrice * applied.tickets).toFixed(2)}</b><small>before theater fees</small></div>
                </div>

                <SeatMap screening={result.winner} ticketCount={applied.tickets} />
              </div>

              <aside className="winner-aside">
                <div className="freshness">
                  <span><i />LIVE INVENTORY</span>
                  <b>Verified just now</b>
                  <small>Fresh for up to 8 seconds</small>
                </div>
                <div className="why-block">
                  <span className="aside-kicker">WHY THIS WINS</span>
                  <h4>Centered without sacrificing viewing depth.</h4>
                  <ul>
                    <li><b>98</b><span>Horizontal alignment<small>2% from centerline</small></span></li>
                    <li><b>96</b><span>Viewing depth<small>Ideal row band</small></span></li>
                    <li><b>100</b><span>Party fit<small>All seats together</small></span></li>
                    <li><b>100</b><span>Geometry confidence<small>{result.winner.confidence}</small></span></li>
                  </ul>
                </div>
                <a className="booking-link" href="https://example.com" target="_blank" rel="noreferrer">
                  Open theater page <span>↗</span>
                </a>
                <p className="booking-note">CenterSeat does not reserve or hold seats in this query-only release.</p>
              </aside>
            </article>

            <div className="alternatives-heading">
              <div><span className="aside-kicker">STRONG ALTERNATIVES</span><h3>Trade time, price, or format.</h3></div>
              <span>Every alternative is live-checked</span>
            </div>
            <div className="alternative-grid">
              {result.alternatives.map((screening, index) => (
                <article className="alternative-card" key={screening.id}>
                  <div className="alt-top"><span>0{index + 2}</span><b>{screening.score}<small>/100</small></b></div>
                  <h4>{seatLabels(screening, applied.tickets).join(" + ")}</h4>
                  <p>{screening.venue}</p>
                  <div className="alt-facts"><span>{screening.time}</span><span>{screening.format}</span><span>{screening.distance} mi</span></div>
                  <div className="alt-bottom"><span>${(screening.ticketPrice * applied.tickets).toFixed(2)} total</span><button type="button">Inspect map →</button></div>
                </article>
              ))}
            </div>
          </>
        ) : (
          <div className="empty-result">
            <span>NO EXACT MATCH</span>
            <h3>Your constraints are doing their job.</h3>
            <p>Try adding another format, widening the time rule, or raising the total-price ceiling. CenterSeat will not silently relax a hard constraint.</p>
            <button onClick={() => { setDraft(defaultQuery); setApplied(defaultQuery); }} type="button">Restore a broad query</button>
          </div>
        )}
      </section>

      <section className="method-section" id="method">
        <div className="method-intro">
          <span className="eyebrow"><span>03</span> Built for repeatability</span>
          <h2>Fast because it asks<br />expensive questions last.</h2>
          <p>Discovery is cached broadly. Live inventory is requested only for the screenings that survive your hard constraints. The exact winner is then refreshed one final time.</p>
        </div>
        <ol className="method-steps">
          <li><span>01</span><div><b>Discover broadly</b><p>Licensed showtime feeds are normalized behind replaceable provider adapters.</p></div><small>5–60 min cache</small></li>
          <li><span>02</span><div><b>Prune cheaply</b><p>Time, distance, price, format, accessibility, and amenity constraints reduce fan-out.</p></div><small>0 network calls</small></li>
          <li><span>03</span><div><b>Check in parallel</b><p>Only the top candidates receive bounded, concurrent live-inventory requests.</p></div><small>5–12 maps</small></li>
          <li><span>04</span><div><b>Rank with geometry</b><p>Real coordinates beat seat labels; every score includes a confidence grade.</p></div><small>Explainable</small></li>
          <li><span>05</span><div><b>Verify the winner</b><p>A final read-only refresh prevents stale recommendations without creating holds.</p></div><small>0–8 sec old</small></li>
        </ol>
      </section>

      <footer>
        <a className="brand footer-brand" href="#top"><span className="brand-mark">C</span><span>CENTERSEAT</span></a>
        <p>Exact seats. Explicit constraints. No guesswork.</p>
        <span>QUERY RELEASE · 2026</span>
      </footer>
    </main>
  );
}
