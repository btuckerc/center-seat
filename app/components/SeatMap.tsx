import type { Recommendation, Seat } from "../lib/api";

const statusLabel: Record<Seat["status"], string> = {
  available: "Available",
  sold: "Occupied",
  held: "Temporarily held",
  broken: "Out of service",
  house: "Blocked by theater",
};

export function SeatMap({ recommendation }: { recommendation: Recommendation }) {
  const selected = new Set(recommendation.seats.map((seat) => seat.id));
  const map = recommendation.seat_map;
  if (!map?.seats.length) {
    return (
      <div className="seat-map unavailable-map">
        The inventory provider did not return a renderable seat layout.
      </div>
    );
  }

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
          const accessible = seat.type === "wheelchair" || seat.type === "companion";
          return (
            <span
              aria-label={`${seat.label}, ${isSelected ? "recommended" : statusLabel[seat.status]}${accessible ? ", accessible" : ""}`}
              className={`map-seat status-${seat.status}${isSelected ? " recommended" : ""}${accessible ? " accessible-seat" : ""}`}
              key={seat.id}
              role="img"
              style={{ left: `${6 + seat.x * 88}%`, top: `${8 + seat.y * 82}%` }}
              title={`${seat.label} · ${isSelected ? "recommended" : statusLabel[seat.status]}`}
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
        <span><i className="map-seat recommended" />Your best</span>
        <span><i className="map-seat accessible-seat" />Accessible</span>
      </div>
      <div className="map-confidence">
        <span>Centerline and target are derived from provider geometry</span>
        <b>{map.confidence.replaceAll("_", " ")}</b>
      </div>
    </div>
  );
}
