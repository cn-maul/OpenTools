import type { CSSProperties } from "react";

import type { FactItem, MeterItem, Tone } from "../types";

type MetricViewProps = {
  metrics?: MeterItem[];
  facts?: FactItem[];
  label?: string;
};

function toneVar(tone?: Tone) {
  switch (tone) {
    case "success":
      return "var(--success)";
    case "warning":
      return "var(--warning)";
    case "danger":
      return "var(--danger)";
    default:
      return "var(--accent)";
  }
}

function ratio(item: MeterItem) {
  const max = item.max && item.max > 0 ? item.max : 100;
  return Math.max(0, Math.min(1, item.value / max));
}

function display(item: MeterItem) {
  const rounded = Number.isInteger(item.value) ? item.value : Math.round(item.value * 10) / 10;
  return `${rounded}${item.unit ?? (item.max === 100 ? "%" : "")}`;
}

export default function MetricView({ metrics = [], facts = [], label }: MetricViewProps) {
  const rings = metrics.filter((item) => (item.variant ?? "ring") === "ring");
  const bars = metrics.filter((item) => item.variant === "bar");

  const groups: { name: string; items: FactItem[] }[] = [];
  for (const fact of facts) {
    const name = fact.group ?? "";
    const existing = groups.find((group) => group.name === name);
    if (existing) existing.items.push(fact);
    else groups.push({ name, items: [fact] });
  }

  return (
    <div className="app-metric-view" aria-label={label ?? "指标"}>
      {rings.length > 0 && (
        <div className="app-metric-rings">
          {rings.map((item) => {
            const style = {
              "--meter-color": toneVar(item.tone),
              "--meter-angle": `${ratio(item) * 360}deg`,
            } as CSSProperties;
            return (
              <div key={item.label} className="app-metric-ring" style={style}>
                <div className="app-metric-ring__dial">
                  <span className="app-metric-ring__value">{display(item)}</span>
                </div>
                <span className="app-metric-ring__label">{item.label}</span>
                {item.detail && <span className="app-metric-ring__detail">{item.detail}</span>}
              </div>
            );
          })}
        </div>
      )}

      {bars.length > 0 && (
        <div className="app-metric-bars">
          {bars.map((item) => {
            const max = item.max && item.max > 0 ? item.max : 100;
            const style = {
              "--meter-color": toneVar(item.tone),
              "--meter-ratio": `${ratio(item) * 100}%`,
            } as CSSProperties;
            return (
              <div key={item.label} className="app-metric-bar" style={style}>
                <div className="app-metric-bar__head">
                  <span className="app-metric-bar__label">{item.label}</span>
                  <span className="app-metric-bar__value">{item.detail ?? display(item)}</span>
                </div>
                <div className="app-metric-bar__track">
                  {item.segments?.length ? (
                    <span className="app-metric-bar__segments">
                      {item.segments.map((segment) => (
                        <span
                          key={segment.label}
                          className="app-metric-bar__segment"
                          style={{
                            width: `${Math.max(0, Math.min(100, (segment.value / max) * 100))}%`,
                            background: toneVar(segment.tone),
                          }}
                          title={segment.label}
                        />
                      ))}
                    </span>
                  ) : (
                    <span className="app-metric-bar__fill" />
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {groups.length > 0 && (
        <div className="app-metric-facts">
          {groups.map((group) => (
            <div key={group.name} className="app-metric-fact-group">
              {group.name && <span className="app-metric-fact-group__name">{group.name}</span>}
              <dl className="app-metric-fact-list">
                {group.items.map((fact, index) => (
                  <div key={`${group.name}-${index}`} className="app-metric-fact">
                    <dt>{fact.label}</dt>
                    <dd>{fact.value}</dd>
                  </div>
                ))}
              </dl>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
