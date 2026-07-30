import type { Screening } from "../lib/demo";

export function SeatMap({
  screening,
  ticketCount,
}: {
  screening: Screening;
  ticketCount: number;
}) {
  const selected = new Set(
    Array.from(
      { length: ticketCount },
      (_, index) => `${screening.row}${screening.startSeat + index}`,
    ),
  );
  const rows = Array.from({ length: 9 }, (_, index) =>
    String.fromCharCode(65 + index),
  );

  return (
    <div className="seat-map" aria-label="Seat map for the winning screening">
      <div className="screen-wrap" aria-hidden="true">
        <span>SCREEN</span>
        <div className="screen" />
      </div>
      <div className="seat-grid">
        {rows.map((row, rowIndex) => (
          <div className="seat-row" key={row}>
            <span className="row-label">{row}</span>
            <div className="seat-row-inner">
              {Array.from({ length: 14 }, (_, columnIndex) => {
                const label = `${row}${columnIndex + 1}`;
                const sold =
                  (rowIndex * 17 + columnIndex * 11 + screening.id.length) % 13 ===
                    4 ||
                  (row === "E" && [3, 12].includes(columnIndex + 1));
                const accessible = row === "I" && [2, 13].includes(columnIndex + 1);
                const state = selected.has(label)
                  ? "selected"
                  : sold
                    ? "sold"
                    : accessible
                      ? "accessible"
                      : "available";
                return (
                  <span
                    aria-label={`${label}, ${state}`}
                    className={`seat ${state}`}
                    key={label}
                    role="img"
                    title={`${label} · ${state}`}
                  />
                );
              })}
            </div>
            <span className="row-label">{row}</span>
          </div>
        ))}
      </div>
      <div className="seat-legend" aria-label="Seat map legend">
        <span><i className="seat available" />Available</span>
        <span><i className="seat selected" />Your best</span>
        <span><i className="seat sold" />Unavailable</span>
        <span><i className="seat accessible" />Accessible</span>
      </div>
    </div>
  );
}
