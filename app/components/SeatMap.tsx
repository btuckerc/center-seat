import type { CSSProperties } from "react";
import type { Recommendation, Seat } from "../lib/api";

const statusLabel: Record<Seat["status"], string> = {
  available: "Available",
  sold: "Occupied",
  held: "Temporarily held",
  broken: "Out of service",
  house: "Blocked by theater",
};

/** Map-space → stage percentage. The stage keeps a gutter for row labels and the screen glow. */
const left = (x: number) => 6 + x * 88;
const top = (y: number) => 6 + y * 86;

/** Smallest positive gap between neighbouring values, ignoring sub-pixel noise. */
function minimumGap(values: number[]) {
  const sorted = [...values].sort((a, b) => a - b);
  let gap = Infinity;
  for (let index = 1; index < sorted.length; index += 1) {
    const delta = sorted[index] - sorted[index - 1];
    if (delta > .004 && delta < gap) gap = delta;
  }
  return gap;
}

export function SeatMap({ recommendation }: { recommendation: Recommendation }) {
  const options = recommendation.seat_options?.length ? recommendation.seat_options : recommendation.seats;
  const selected = new Set(options.map((seat) => seat.id));
  const primary = new Set(recommendation.seats.map((seat) => seat.id));
  const map = recommendation.seat_map;
  if (!map?.seats.length) {
    return <div className="seat-map is-empty" role="img" aria-label="No seat layout returned for this screening" />;
  }
  const recommendedZone = new Set(map.recommended_zone ?? options.map((seat) => seat.id));

  // Group seats into horizontal bands. Pseudo-rows (e.g. "WC" spaces) can span or share bands, so each band gets at most
  // one label: the single-band row with the most seats. Accessible spaces keep their own marker instead.
  const bands = new Map<number, number[]>();
  const rowBands = new Map<string, { bands: Set<number>; seats: number }>();
  for (const seat of map.seats) {
    const band = Math.round(seat.y * 200) / 200;
    const xs = bands.get(band);
    if (xs) xs.push(seat.x);
    else bands.set(band, [seat.x]);
    const row = rowBands.get(seat.row);
    if (row) {
      row.bands.add(band);
      row.seats += 1;
    } else {
      rowBands.set(seat.row, { bands: new Set([band]), seats: 1 });
    }
  }
  const bandLabels = new Map<number, { row: string; seats: number }>();
  for (const [row, info] of rowBands) {
    if (info.bands.size !== 1) continue;
    const [band] = info.bands;
    const current = bandLabels.get(band);
    if (!current || info.seats > current.seats) bandLabels.set(band, { row, seats: info.seats });
  }
  const rows = [...bandLabels].map(([band, { row }]) => [row, band] as const).sort((a, b) => a[1] - b[1]);

  // Size seats from the typical tightest pitch: the median band's minimum gap ignores odd pairs like adjacent wheelchair spaces.
  const bandGaps = [...bands.values()].map(minimumGap).filter(Number.isFinite).sort((a, b) => a - b);
  const columnGap = Math.min(.12, bandGaps[Math.floor(bandGaps.length / 2)] ?? .12);
  const rowGap = Math.min(.25, minimumGap([...bands.keys()]));
  const seatWidth = columnGap * 88 * .78;
  const aspect = Math.min(2.4, Math.max(.95, (rowGap * 86 * .74 * 1.15) / (seatWidth || 1)));
  const present = new Set<string>(map.seats.map((seat) => seat.status));
  const hasAccessible = map.seats.some((seat) => seat.type === "wheelchair" || seat.type === "companion");
  const hasAlternatives = options.some((seat) => !primary.has(seat.id));

  return (
    // --stage-max keeps sparse auditoriums from inflating seats past ~44px; the screen arc shares it.
    <figure className="seat-map" aria-label="Live seat map" style={{ "--aspect": aspect, "--stage-max": `${Math.round(4_400 / seatWidth)}px` } as CSSProperties}>
      <div aria-hidden="true" className="screen" />
      <div className="seat-map-stage" style={{ aspectRatio: aspect, "--seat-w": `${seatWidth}%` } as CSSProperties}>
        {map.preferred_zone_bounds ? (
          <div
            aria-hidden="true"
            className="preferred-zone"
            style={{
              left: `${left(map.preferred_zone_bounds.minimum_x)}%`,
              top: `${top(map.preferred_zone_bounds.minimum_y)}%`,
              width: `${(map.preferred_zone_bounds.maximum_x - map.preferred_zone_bounds.minimum_x) * 88}%`,
              height: `${(map.preferred_zone_bounds.maximum_y - map.preferred_zone_bounds.minimum_y) * 86}%`,
            }}
          />
        ) : null}
        {map.preferred_depth ? (
          <div
            aria-hidden="true"
            className="preferred-band"
            style={{ top: `${top(map.preferred_depth.minimum)}%`, height: `${(map.preferred_depth.maximum - map.preferred_depth.minimum) * 86}%` }}
          />
        ) : null}
        <div aria-hidden="true" className="centerline" style={{ left: `${left(map.target.x)}%` }} />
        <div aria-hidden="true" className="target" style={{ left: `${left(map.target.x)}%`, top: `${top(map.target.y)}%` }} />
        {rows.map(([row, y]) => <span aria-hidden="true" className="row-label" key={row} style={{ top: `${top(y)}%` }}>{row}</span>)}
        {map.seats.map((seat) => {
          const isSelected = selected.has(seat.id);
          const isPrimary = primary.has(seat.id);
          const isAlternative = isSelected && !isPrimary;
          const isInZone = recommendedZone.has(seat.id);
          const accessible = seat.type === "wheelchair" || seat.type === "companion";
          const description = isPrimary ? "best available" : isAlternative ? "strong alternative" : isInZone ? `ideal zone, ${statusLabel[seat.status]}` : statusLabel[seat.status];
          return (
            <span
              aria-label={`${seat.label}, ${description}${accessible ? `, ${seat.type} space` : ""}`}
              className={`seat status-${seat.status}${isInZone ? " in-zone" : ""}${isPrimary ? " is-best" : ""}${isAlternative ? " is-alt" : ""}`}
              key={seat.id}
              role="img"
              style={{ left: `${left(seat.x)}%`, top: `${top(seat.y)}%` }}
              title={`${seat.label} · ${description}`}
            >
              {accessible ? <i className="seat-access" /> : null}
              {isPrimary ? <b className="seat-tag">{seat.label}</b> : null}
            </span>
          );
        })}
      </div>
      <figcaption className="legend">
        {present.has("available") ? <span><i className="seat status-available" />Open</span> : null}
        {present.has("sold") ? <span><i className="seat status-sold" />Taken</span> : null}
        {present.has("held") ? <span><i className="seat status-held" />Held</span> : null}
        {present.has("house") || present.has("broken") ? <span><i className="seat status-house" />Blocked</span> : null}
        <span><i className="seat is-best" />Best</span>
        {hasAlternatives ? <span><i className="seat is-alt" />Alt</span> : null}
        {hasAccessible ? <span><i className="seat status-available"><i className="seat-access" /></i>Access</span> : null}
      </figcaption>
    </figure>
  );
}
