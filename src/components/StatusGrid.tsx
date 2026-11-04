import type { StatusCell, StatusLegendItem, Tone } from "../types";

type StatusGridProps = {
  cells: StatusCell[];
  legend?: StatusLegendItem[];
  label?: string;
  labeled?: boolean;
};

function toneClass(tone?: Tone) {
  switch (tone) {
    case "success":
      return "status-grid__cell--success";
    case "warning":
      return "status-grid__cell--warning";
    case "danger":
      return "status-grid__cell--danger";
    case "neutral":
      return "status-grid__cell--neutral";
    default:
      return "status-grid__cell--muted";
  }
}

export default function StatusGrid({ cells, legend = [], label, labeled = false }: StatusGridProps) {
  return (
    <div className="status-grid" aria-label={label ?? "状态网格"}>
      {legend.length > 0 && (
        <div className="status-grid__legend">
          {legend.map((item) => (
            <span key={item.label} className="status-grid__legend-item">
              <span
                className={`status-grid__legend-dot ${
                  item.category ? `status-grid__cell--${item.category}` : toneClass(item.tone)
                }`}
              />
              {item.label}
            </span>
          ))}
        </div>
      )}
      <div className={`status-grid__cells ${labeled ? "status-grid__cells--labeled" : ""}`}>
        {cells.map((cell, index) => (
          <span
            key={`${cell.label}-${index}`}
            className={[
              "status-grid__cell",
              toneClass(cell.tone),
              cell.category ? `status-grid__cell--${cell.category}` : "",
            ]
              .filter(Boolean)
              .join(" ")}
            title={cell.detail ? `${cell.label} ${cell.detail}` : cell.label}
          >
            {labeled && (
              <>
                <span className="status-grid__cell-label">{cell.label}</span>
                {cell.detail && <span className="status-grid__cell-detail">{cell.detail}</span>}
              </>
            )}
          </span>
        ))}
      </div>
    </div>
  );
}
