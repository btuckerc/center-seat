import type { Recommendation, SeatQueryResponse } from "../lib/api";
import { LockIcon } from "./icons";

const humanize = (value: string) => value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());

const steps = [
  ["Discover", "Every matching date and showtime"],
  ["Filter", "Time, distance, format, price, access"],
  ["Compare", "Live seat maps, checked in parallel"],
  ["Prove", "Stops only when nothing left can score higher"],
  ["Recheck", "The winner gets a final live read"],
];

/** Explanation, price, coverage and method — everything secondary to the seat map, in headed sections. */
export function InfoContent({ recommendation, coverage }: { recommendation?: Recommendation; coverage?: SeatQueryResponse["coverage"] }) {
  const failed = coverage ? coverage.inventories_failed ?? coverage.providers_degraded ?? 0 : 0;
  const reasons = coverage ? Object.entries(coverage.inventory_failure_reasons ?? {}).filter(([, count]) => count > 0) : [];
  const price = recommendation?.price;
  const [lead, ...more] = (recommendation?.explanation ?? []).map((line) => /[.!?]$/.test(line) ? line : `${line}.`);
  const money = (value: number | null) => value === null ? "Unknown" : price?.currency ? new Intl.NumberFormat("en-US", { style: "currency", currency: price.currency }).format(value) : String(value);

  return (
    <div className="info">
      {recommendation ? (
        <section>
          <h3>Why this seat</h3>
          {lead ? <p>{lead}</p> : null}
          {more.length ? (
            <details className="more">
              <summary>More</summary>
              <p>{more.join(" ")}</p>
            </details>
          ) : null}
          <ul className="breakdown">
            {Object.entries(recommendation.score_breakdown).map(([key, value]) => (
              <li key={key}>
                <span>{humanize(key)}</span>
                <i aria-hidden="true"><i style={{ width: `${Math.max(0, Math.min(100, value))}%` }} /></i>
                <b>{Math.round(value)}</b>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {price ? (
        <section>
          <h3>Price estimate</h3>
          <dl className="stats">
            <div><dt>Total · {price.ticket_count} ticket{price.ticket_count === 1 ? "" : "s"}</dt><dd>{money(price.estimated_total)}</dd></div>
            <div><dt>Per ticket</dt><dd>{money(price.ticket_price)}</dd></div>
            <div><dt>Fee per ticket</dt><dd>{price.fee_per_ticket === 0 ? "None" : money(price.fee_per_ticket)}</dd></div>
          </dl>
          <p className="muted">{price.qualification || "Estimate; not the final checkout total."}</p>
        </section>
      ) : null}

      {coverage ? (
        <section>
          <h3>Coverage</h3>
          <dl className="stats">
            <div><dt>Dates</dt><dd>{coverage.dates_compared}/{coverage.dates_with_screenings}</dd></div>
            <div><dt>Showtimes</dt><dd>{coverage.screenings_discovered}</dd></div>
            <div><dt>Maps</dt><dd>{coverage.inventories_fresh}/{coverage.inventories_checked}</dd></div>
            <div><dt>No match</dt><dd>{coverage.screenings_unavailable ?? 0}</dd></div>
            <div className={failed ? "is-bad" : ""}><dt>Interrupted</dt><dd>{failed}</dd></div>
            <div><dt>Time</dt><dd>{(coverage.elapsed_ms / 1_000).toFixed(1)}s</dd></div>
          </dl>
          <p className={coverage.range_best_proven ? "proof is-good" : "proof"}>
            {coverage.range_best_proven
              ? recommendation ? "No remaining screening could beat this." : "Every screening was checked."
              : recommendation ? "Best found; not every screening could be ruled out." : "Not every screening could be checked."}
          </p>
          {reasons.length ? <p className="muted">{reasons.map(([reason, count]) => `${count} ${humanize(reason).toLowerCase()}`).join(" · ")}</p> : null}
          {recommendation?.seat_map ? <p className="muted">Seat geometry: {recommendation.seat_map.confidence.replaceAll("_", " ")}</p> : null}
        </section>
      ) : null}

      <section>
        {recommendation || coverage ? <h3>How it works</h3> : null}
        <ol className="steps">
          {steps.map(([title, detail]) => <li key={title}><b>{title}</b><span>{detail}</span></li>)}
        </ol>
      </section>

      <p className="read-only"><LockIcon size={16} />Read-only. No cart, hold, or purchase.</p>
    </div>
  );
}
