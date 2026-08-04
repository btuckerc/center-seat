import type { Recommendation, Seat } from "../lib/api";

const statusLabel: Record<Seat["status"], string> = {
  available: "Available",
  sold: "Occupied",
  held: "Temporarily held",
  broken: "Out of service",
  house: "Blocked by theater",
};

export function SeatMap({ recommendation }: { recommendation: Recommendation }) {
  const options = recommendation.seat_options?.length ? recommendation.seat_options : recommendation.seats;
  const selected = new Set(options.map((seat) => seat.id));
  const primary = new Set(recommendation.seats.map((seat) => seat.id));
  const map = recommendation.seat_map;
  if (!map?.seats.length) {
    return (
      <div className="seat-map unavailable-map">
        The inventory provider did not return a renderable seat layout.
      </div>
    );
  }
  const recommendedZone = new Set(map.recommended_zone ?? options.map((seat) => seat.id));

  const rows = Array.from(
    map.seats.reduce((values, seat) => {
      if (!values.has(seat.row)) values.set(seat.row, seat.y);
      return values;
    }, new Map<string, number>()),
  ).sort((a, b) => a[1] - b[1]);

  return (
    <div className="seat-map" aria-label="Live seat map for the winning screening">
      <div className="screen-wrap" aria-hidden="true">
        <span>SCREEN</span>
        <div className="screen" />
      </div>
      <div className="seat-map-stage">
        {map.preferred_zone_bounds ? (
          <div
            className="preferred-seat-zone"
            style={{
              left: `${6 + map.preferred_zone_bounds.minimum_x * 88}%`,
              top: `${8 + map.preferred_zone_bounds.minimum_y * 82}%`,
              width: `${(map.preferred_zone_bounds.maximum_x - map.preferred_zone_bounds.minimum_x) * 88}%`,
              height: `${(map.preferred_zone_bounds.maximum_y - map.preferred_zone_bounds.minimum_y) * 82}%`,
            }}
            aria-hidden="true"
          ><span>Custom preference</span></div>
        ) : null}
        {map.preferred_depth ? (
          <div
            className="preferred-depth-band"
            style={{
              top: `${8 + map.preferred_depth.minimum * 82}%`,
              height: `${(map.preferred_depth.maximum - map.preferred_depth.minimum) * 82}%`,
            }}
            aria-hidden="true"
          ><span>Preferred viewing zone</span></div>
        ) : null}
        <div
          className="geometry-centerline"
          style={{ left: `${6 + map.target.x * 88}%` }}
          aria-hidden="true"
        />
        <div
          className="geometry-target"
          style={{ left: `${6 + map.target.x * 88}%`, top: `${8 + map.target.y * 82}%` }}
          aria-hidden="true"
        >
          <span>target</span>
        </div>
        {rows.map(([row, y]) => (
          <span className="map-row-label" key={row} style={{ top: `${8 + y * 82}%` }}>{row}</span>
        ))}
        {map.seats.map((seat) => {
          const isSelected = selected.has(seat.id);
          const isPrimary = primary.has(seat.id);
          const isStrongOption = isSelected && !isPrimary;
          const isInZone = recommendedZone.has(seat.id);
          const accessible = seat.type === "wheelchair" || seat.type === "companion";
          const description = isPrimary
            ? "best available option"
            : isStrongOption
              ? "strong alternative"
            : isInZone
              ? `ideal-zone position, ${statusLabel[seat.status]}`
              : statusLabel[seat.status];
          return (
            <span
              aria-label={`${seat.label}, ${description}${accessible ? ", accessible" : ""}`}
              className={`map-seat status-${seat.status}${isInZone ? " ideal-zone-seat" : ""}${isPrimary ? " recommended" : ""}${isStrongOption ? " strong-option" : ""}${accessible ? " accessible-seat" : ""}`}
              key={seat.id}
              role="img"
              style={{ left: `${6 + seat.x * 88}%`, top: `${8 + seat.y * 82}%` }}
              title={`${seat.label} · ${description}`}
            >
              {isSelected ? <b>{seat.label}</b> : null}
            </span>
          );
        })}
      </div>
      <div className="seat-legend" aria-label="Seat map legend">
        <span><i className="map-seat status-available" />Available</span>
        <span><i className="map-seat status-sold" />Occupied</span>
        <span><i className="map-seat status-held" />Held</span>
        <span><i className="map-seat status-house" />Blocked</span>
        <span><i className="map-seat recommended" />Best available</span>
        <span><i className="map-seat strong-option" />Strong alternative</span>
        <span><i className="map-seat accessible-seat" />Accessible</span>
      </div>
      <div className="map-confidence">
        <span>Centerline and target are derived from provider geometry</span>
        <b>{map.confidence.replaceAll("_", " ")}</b>
      </div>
    </div>
  );
}
