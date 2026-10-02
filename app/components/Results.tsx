"use client";

import { useEffect, useRef, useState } from "react";
import {
  buildCheckoutHandoff,
  formatShowtime,
  handoffText,
  type QueryState,
  type Recommendation,
  type SeatQueryResponse,
  type ShowtimeQueryResponse,
} from "../lib/api";
import { searchParamsFromQuery } from "../lib/share";
import { InfoContent } from "./InfoSheet";
import { AlertIcon, ArrowRightIcon, ClockIcon, CodeIcon, ExternalIcon, FilmIcon, InfoIcon, LinkIcon, LockIcon, PinIcon, RefreshIcon, ShareIcon, TicketIcon } from "./icons";
import { SeatMap } from "./SeatMap";
import { Sheet } from "./Sheet";

const humanize = (value: string) => value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
const seatLabels = (recommendation: Recommendation) => (recommendation.seat_options?.length ? recommendation.seat_options : recommendation.seats).map((seat) => seat.label);
// Module-level clock reads: event handlers may consult wall time; render reads the ticking `now` state.
const currentTime = () => Date.now();
const isPast = (iso: string) => currentTime() >= new Date(iso).getTime();
/** The live dot stays "Live" this long after a check. */
const freshSeconds = 30;
/** Matches the server snapshot window; older cached maps are re-checked before reuse. */
const snapshotSeconds = 8;

function estimateAmount(price: Recommendation["price"] | undefined) {
  if (price?.estimated_total == null || !price.currency) return null;
  return new Intl.NumberFormat("en-US", { style: "currency", currency: price.currency, maximumFractionDigits: 0 }).format(price.estimated_total);
}

/** Compact "≈ $50" ("≈ $46, fees unknown" when fees are unknown); null when the price is unknown, never $0. */
function estimateLabel(price: Recommendation["price"] | undefined) {
  const amount = estimateAmount(price);
  return price && amount ? `≈ ${amount}${price.fees_included ? "" : ", fees unknown"}` : null;
}

/** Spoken form: states it is an estimate, the ticket count, and whether fees are known. */
function estimateDescription(price: Recommendation["price"] | undefined) {
  const amount = estimateAmount(price);
  if (!price || !amount) return "price unknown";
  return `estimated ${amount} for ${price.ticket_count} ticket${price.ticket_count === 1 ? "" : "s"}, ${price.fees_included ? "fees included" : "fees unknown"}, not the final checkout total`;
}

class RefreshError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

function shareLink(applied: QueryState) {
  const url = new URL("/", window.location.origin);
  url.search = searchParamsFromQuery(applied).toString();
  return url.toString();
}

/** Clipboard write with a prompt fallback; resolves to whether the clipboard took it. */
async function copyText(text: string, promptLabel: string) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    window.prompt(promptLabel, text);
    return false;
  }
}

function ScoreRing({ score }: { score: number }) {
  const circumference = 2 * Math.PI * 22;
  const shown = Math.round(score * 10) / 10;
  return (
    <div aria-label={`Match ${shown} of 100`} className="score-ring" role="img">
      <svg aria-hidden="true" viewBox="0 0 52 52">
        <circle className="score-track" cx="26" cy="26" r="22" />
        <circle className="score-fill" cx="26" cy="26" r="22" strokeDasharray={circumference} style={{ strokeDashoffset: circumference * (1 - Math.max(0, Math.min(100, score)) / 100) }} />
      </svg>
      <b>{shown}</b>
    </div>
  );
}

/** Disclosure of plain buttons: Escape and item activation hand focus back to the trigger. */
export function ShareMenu({ items }: { items: { icon: React.ReactNode; label: string; run: () => void }[] }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (event: MouseEvent | KeyboardEvent) => {
      if (event instanceof KeyboardEvent) {
        if (event.key !== "Escape") return;
        if (root.current?.contains(document.activeElement)) trigger.current?.focus();
        setOpen(false);
      } else if (!root.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", close);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", close);
    };
  }, [open]);

  return (
    <div className="menu" onBlur={(event) => { if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node)) setOpen(false); }} ref={root}>
      <button aria-expanded={open} aria-label="Share" className="icon-button" onClick={() => setOpen((value) => !value)} ref={trigger} type="button"><ShareIcon /></button>
      {open ? (
        <div className="menu-list">
          {items.map((item) => (
            <button key={item.label} onClick={() => { setOpen(false); trigger.current?.focus(); item.run(); }} type="button">{item.icon}<span>{item.label}</span></button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function LiveResult({ result, applied, onRerun, notify }: { result: SeatQueryResponse; applied: QueryState; onRerun: () => void; notify: (message: string) => void }) {
  const winner = result.winner as Recommendation;
  const [active, setActive] = useState<Recommendation>(winner);
  const [loaded, setLoaded] = useState<Record<number, Recommendation>>({ [winner.rank]: winner });
  const [loadingRank, setLoadingRank] = useState<number | null>(null);
  const [mapProblem, setMapProblem] = useState("");
  const [serverExpired, setServerExpired] = useState(false);
  const [infoOpen, setInfoOpen] = useState(false);
  // 0 until the first client tick keeps render pure; ages read as fresh for that first second.
  const [now, setNow] = useState(0);
  const [changedSeats, setChangedSeats] = useState<{ before: string[]; after: string[] } | null>(null);
  const [unavailableRanks, setUnavailableRanks] = useState<Record<number, true>>({});
  const expired = serverExpired || (now > 0 && now >= new Date(result.refresh_until).getTime());

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, []);

  // Alerts live in document flow (not the fixed mobile CTA), so bring a new one into view where the user acted.
  const notes = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!changedSeats && !mapProblem) return;
    notes.current?.scrollIntoView({ block: "nearest", behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  }, [changedSeats, mapProblem]);

  const ageSeconds = (recommendation: Recommendation, at = now) => Math.max(0, Math.floor((at - new Date(recommendation.verified_at).getTime()) / 1_000));
  /** A cached map may be reused only inside the snapshot window (`expires_at`, ~8s). */
  const pastSnapshot = (recommendation: Recommendation) => isPast(result.expires_at) || ageSeconds(recommendation, currentTime()) >= snapshotSeconds;

  const refreshRecommendation = async (rank: number) => {
    const response = await fetch(`/api/seat-recommendations?query_id=${encodeURIComponent(result.query_id)}&rank=${rank}`, { cache: "no-store" });
    const body = await response.json();
    if (!response.ok) {
      if (response.status === 410) setServerExpired(true);
      if (response.status === 409) setUnavailableRanks((current) => ({ ...current, [rank]: true }));
      throw new RefreshError(response.status, body.detail ?? body.title ?? "The live check failed.");
    }
    return body as Recommendation;
  };

  /** `action` names what did not happen, e.g. "No booking handoff was opened." */
  const reportRefreshFailure = (error: unknown, action: string) => {
    if (!(error instanceof RefreshError)) setMapProblem("The live check failed.");
    else if (error.status === 409) setMapProblem(`These seats are gone. ${action}`);
    else if (error.status === 410) setMapProblem("");
    else if (error.status === 503) setMapProblem("Live provider unavailable. Try again.");
    else setMapProblem(error.message);
  };

  const openRecommendation = async (recommendation: Recommendation) => {
    if (expired || isPast(result.refresh_until)) {
      setServerExpired(true);
      return;
    }
    const cached = loaded[recommendation.rank] ?? recommendation;
    const stale = pastSnapshot(cached) || !cached.seat_map;
    if (cached.rank === active.rank && !stale) return;
    setMapProblem("");
    setChangedSeats(null);
    setLoadingRank(recommendation.rank);
    try {
      const refreshed = stale ? await refreshRecommendation(recommendation.rank) : cached;
      setLoaded((current) => ({ ...current, [refreshed.rank]: refreshed }));
      setActive(refreshed);
    } catch (error) {
      reportRefreshFailure(error, "Pick another option.");
    } finally {
      setLoadingRank(null);
    }
  };

  /** Always re-verifies the active seats before navigating; the tab opens synchronously to survive popup blockers. */
  const continueAtProvider = async () => {
    const tab = window.open("about:blank", "_blank");
    if (!tab) {
      setMapProblem("Allow pop-ups to continue to the provider.");
      return;
    }
    tab.opener = null;
    if (expired || isPast(result.refresh_until)) {
      setServerExpired(true);
      tab.close();
      return;
    }
    setMapProblem("");
    setChangedSeats(null);
    setLoadingRank(active.rank);
    try {
      const refreshed = await refreshRecommendation(active.rank);
      setLoaded((current) => ({ ...current, [refreshed.rank]: refreshed }));
      setActive(refreshed);
      const before = active.seats.map((seat) => seat.label);
      const after = refreshed.seats.map((seat) => seat.label);
      if (before.length !== after.length || before.some((label, index) => label !== after[index])) {
        tab.close();
        setChangedSeats({ before, after });
      } else {
        const destination = refreshed.booking_url || refreshed.showtime.booking_url;
        if (destination) tab.location.href = destination;
        else {
          tab.close();
          setMapProblem("No provider booking link was returned.");
        }
      }
    } catch (error) {
      tab.close();
      reportRefreshFailure(error, "Nothing was opened.");
    } finally {
      setLoadingRank(null);
    }
  };

  /** Handoffs name exact seats, so they are re-verified before copying, like the provider handoff. */
  const copyHandoff = async (json: boolean) => {
    if (expired || isPast(result.refresh_until)) {
      setServerExpired(true);
      return;
    }
    setMapProblem("");
    setLoadingRank(active.rank);
    let refreshed: Recommendation;
    try {
      refreshed = await refreshRecommendation(active.rank);
      setLoaded((current) => ({ ...current, [refreshed.rank]: refreshed }));
      setActive(refreshed);
    } catch (error) {
      reportRefreshFailure(error, "Nothing was copied.");
      return;
    } finally {
      setLoadingRank(null);
    }
    const handoff = buildCheckoutHandoff(refreshed, applied.tickets, applied.timezone, shareLink(applied));
    const copied = await copyText(json ? JSON.stringify(handoff, null, 2) : handoffText(handoff), json ? "Copy agent JSON" : "Copy checkout details");
    if (copied) notify(json ? "Agent JSON copied" : "Checkout copied");
  };

  const labels = seatLabels(active);
  const showtime = formatShowtime(active.showtime.starts_at, applied.timezone);
  const choices = [winner, ...result.alternatives].map((recommendation) => loaded[recommendation.rank] ?? recommendation);
  const bookingURL = active.booking_url || active.showtime.booking_url;
  const age = ageSeconds(active);
  const fresh = age <= freshSeconds;
  const price = estimateLabel(active.price);
  const busy = loadingRank !== null;

  return (
    <div className="result">
      <section className="stage">
        <header className="stage-head">
          <div className="stage-title">
            <div className="badges">
              <span className={`live-dot${expired ? " is-expired" : fresh ? " is-fresh" : " is-aged"}`} title={expired ? "Expired" : `Checked ${age < 60 ? `${age}s` : `${Math.floor(age / 60)}m`} ago`}>
                <i aria-hidden="true" />{expired ? "Expired" : fresh ? "Live" : `${age < 60 ? `${age}s` : `${Math.floor(age / 60)}m`}`}
              </span>
              {active.rank === 1 ? <span className="badge-pill is-best">{result.coverage.range_best_proven ? "Best across range" : "Best found"}</span> : <span className="badge-pill">#{active.rank}</span>}
              {active.profile_match === "closest_fallback" ? <span className="badge-pill is-warn" title="No seat sat inside your preferred zone; this is the closest."><AlertIcon size={13} />Closest fallback</span> : null}
            </div>
            <h1 key={labels.join()}>{labels.join(" · ")}</h1>
            <ul className="facts">
              <li><ClockIcon size={16} /><span>{showtime.date} · {showtime.time} {showtime.zone}</span></li>
              <li><PinIcon size={16} /><span>{active.showtime.venue_name}{active.showtime.auditorium_name ? ` · ${active.showtime.auditorium_name}` : ""} · {active.showtime.distance_miles.toFixed(1)} mi</span></li>
              <li><FilmIcon size={16} /><span>{humanize(active.showtime.format)}</span></li>
              <li title={active.price?.qualification}>
                <TicketIcon size={16} />
                {price ? <><span aria-hidden="true">{price} · {active.price.ticket_count} ticket{active.price.ticket_count === 1 ? "" : "s"}</span><span className="sr-only">{estimateDescription(active.price)}</span></> : <span>Price unknown</span>}
              </li>
            </ul>
          </div>
          <div className="stage-tools">
            <ScoreRing score={active.score} />
            <ShareMenu items={[
              { icon: <LinkIcon size={18} />, label: "Copy link", run: () => void copyText(shareLink(applied), "Copy this search link").then((copied) => copied && notify("Link copied")) },
              { icon: <TicketIcon size={18} />, label: "Copy for checkout", run: () => void copyHandoff(false) },
              { icon: <CodeIcon size={18} />, label: "Agent JSON", run: () => void copyHandoff(true) },
            ]} />
            <button aria-label="Details" className="icon-button" onClick={() => setInfoOpen(true)} type="button"><InfoIcon /></button>
          </div>
        </header>
        <div className={`map-frame${expired ? " is-expired" : ""}${busy ? " is-busy" : ""}`}>
          <SeatMap key={`${active.rank}-${active.verified_at}`} recommendation={active} />
          {expired ? (
            <button className="rerun" onClick={onRerun} type="button"><RefreshIcon /><span>Search again</span></button>
          ) : null}
        </div>
      </section>

      <aside className="rail">
        <div className="cta">
          <button className="cta-button" disabled={busy || expired || !bookingURL} onClick={continueAtProvider} type="button">
            <span>{loadingRank === active.rank ? "Checking…" : "Open provider"}</span>
            <ExternalIcon />
          </button>
          <small className="read-only"><LockIcon size={13} />No seats held</small>
        </div>
        {changedSeats || mapProblem || result.warnings?.length ? (
          <div className="notes" ref={notes}>
            {changedSeats ? (
              <div className="alert" role="alert">
                <p className="seat-swap"><s>{changedSeats.before.join(" · ")}</s><ArrowRightIcon size={16} /><b>{changedSeats.after.join(" · ")}</b></p>
                <button onClick={continueAtProvider} type="button">Continue with these</button>
              </div>
            ) : null}
            {mapProblem ? <p className="alert" role="alert"><AlertIcon size={16} />{mapProblem}</p> : null}
            {result.warnings?.map((warning) => <p className="alert is-soft" key={warning}><AlertIcon size={16} />{warning}</p>)}
          </div>
        ) : null}

        <ol aria-label="Options" className="stubs">
          {choices.map((recommendation) => {
            const time = formatShowtime(recommendation.showtime.starts_at, applied.timezone);
            const selected = active.rank === recommendation.rank;
            return (
              <li key={recommendation.rank}>
                <button
                  aria-label={`${recommendation.rank === 1 ? "Best" : `Option ${recommendation.rank}`}: ${time.date} ${time.time}, ${recommendation.showtime.venue_name}, seats ${seatLabels(recommendation).join(", ")}, ${estimateDescription(recommendation.price)}, match ${Math.round(recommendation.score)}${unavailableRanks[recommendation.rank] ? ", no longer available" : ""}`}
                  aria-pressed={selected}
                  className={`stub${selected ? " is-selected" : ""}${recommendation.rank === 1 ? " is-best" : ""}${loadingRank === recommendation.rank ? " is-loading" : ""}${unavailableRanks[recommendation.rank] ? " is-gone" : ""}`}
                  disabled={busy || expired || unavailableRanks[recommendation.rank]}
                  onClick={() => void openRecommendation(recommendation)}
                  type="button"
                >
                  <span className="stub-when"><b>{time.time}</b><small>{time.date}</small></span>
                  <span className="stub-where"><span>{recommendation.showtime.venue_name}</span><small>{seatLabels(recommendation).join(" · ")} · {humanize(recommendation.showtime.format)}{estimateLabel(recommendation.price) ? ` · ${estimateLabel(recommendation.price)}` : ""}</small></span>
                  <span aria-hidden="true" className="stub-score">{Math.round(recommendation.score)}</span>
                </button>
              </li>
            );
          })}
        </ol>
      </aside>

      <Sheet className="info-sheet" label="Details" onClose={() => setInfoOpen(false)} open={infoOpen}>
        <InfoContent coverage={result.coverage} recommendation={active} />
      </Sheet>
    </div>
  );
}

export function ShowtimeResults({ result, applied, notify }: { result: ShowtimeQueryResponse; applied: QueryState; notify: (message: string) => void }) {
  return (
    <div className="showtimes">
      <header className="showtimes-head">
        <p className="notice"><FilmIcon size={16} />Showtimes only — this source has no seat maps.</p>
        <ShareMenu items={[{ icon: <LinkIcon size={18} />, label: "Copy link", run: () => void copyText(shareLink(applied), "Copy this search link").then((copied) => copied && notify("Link copied")) }]} />
      </header>
      <ul className="showtime-grid">
        {result.showtimes.map((showtime) => {
          const labels = formatShowtime(showtime.starts_at, applied.timezone);
          return (
            <li key={showtime.id}>
              {showtime.booking_url ? (
                <a className="showtime" href={showtime.booking_url} rel="noreferrer" target="_blank">
                  <b>{labels.time} <small>{labels.zone}</small></b>
                  <span>{labels.date}</span>
                  <span>{showtime.venue_name}</span>
                  <small>{humanize(showtime.format)} · {showtime.distance_miles.toFixed(1)} mi</small>
                  <ExternalIcon className="showtime-go" size={16} />
                </a>
              ) : (
                <div className="showtime is-static">
                  <b>{labels.time} <small>{labels.zone}</small></b>
                  <span>{labels.date}</span>
                  <span>{showtime.venue_name}</span>
                  <small>{humanize(showtime.format)} · {showtime.distance_miles.toFixed(1)} mi</small>
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
