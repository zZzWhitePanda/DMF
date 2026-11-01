"use client";

import { useMemo, useRef, useState } from "react";
import type { Bucket, Metric } from "@/lib/farm-session";
import { cn, fmtClock, fmtCommas, fmtCompact } from "@/lib/utils";

const MINUTES = 30;

/** Round up to a clean axis maximum: 1, 2 or 5 × 10ⁿ. */
function niceMax(value: number) {
  if (value <= 0) return 100;
  const magnitude = 10 ** Math.floor(Math.log10(value));
  return [1, 2, 5, 10].find((m) => m * magnitude >= value)! * magnitude;
}

/** Keeps a label centered on its bar without running off either end of the plot. */
function anchor(index: number) {
  if (index < 2) return "translateX(-12%)";
  if (index > MINUTES - 3) return "translateX(-88%)";
  return "translateX(-50%)";
}

const center = (index: number) => `${((index + 0.5) / MINUTES) * 100}%`;

// Earnings per minute over the last half hour. One tab stop: arrow keys walk
// the bars, and the same readout appears on hover, touch and keyboard focus.
export function EarningsChart({
  timeline,
  metric,
  now,
  live,
}: {
  timeline: Record<number, Bucket>;
  metric: Metric;
  now: number;
  live: boolean;
}) {
  const plot = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState<number | null>(null);
  const currentMinute = Math.floor(now / 60);

  const bars = useMemo(
    () =>
      Array.from({ length: MINUTES }, (_, i) => {
        const minute = currentMinute - MINUTES + 1 + i;
        return { minute, value: timeline[minute]?.[metric] ?? 0 };
      }),
    [timeline, metric, currentMinute],
  );

  const peak = bars.reduce((best, bar, i) => (bar.value > bars[best].value ? i : best), 0);
  const top = niceMax(bars[peak].value);
  const hasData = bars[peak].value > 0;
  const height = (value: number) => (value / top) * 100;

  const barAt = (clientX: number) => {
    const rect = plot.current?.getBoundingClientRect();
    if (!rect) return null;
    return Math.min(MINUTES - 1, Math.max(0, Math.floor(((clientX - rect.left) / rect.width) * MINUTES)));
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    const step = e.key === "ArrowLeft" ? -1 : e.key === "ArrowRight" ? 1 : 0;
    if (!step) return;
    e.preventDefault();
    setActive((i) => Math.min(MINUTES - 1, Math.max(0, (i ?? MINUTES) + step)));
  };

  const summary = hasData
    ? `Earnings per minute over the last ${MINUTES} minutes. Peak of ${fmtCommas(bars[peak].value)} ${metric} at ${fmtClock(bars[peak].minute * 60)}.`
    : `Earnings per minute over the last ${MINUTES} minutes. Nothing earned yet.`;

  return (
    <figure className="m-0">
      <div className="flex gap-2.5">
        <div className="relative mt-6 h-36 w-9 shrink-0 font-mono text-xs tabular-nums text-fg-subtle" aria-hidden>
          {hasData && (
            <>
              <span className="absolute right-0 top-0 -translate-y-1/2">{fmtCompact(top)}</span>
              <span className="absolute right-0 top-1/2 -translate-y-1/2">{fmtCompact(top / 2)}</span>
              <span className="absolute bottom-0 right-0 translate-y-1/2">0</span>
            </>
          )}
        </div>

        <div
          ref={plot}
          role="img"
          aria-label={summary}
          tabIndex={0}
          onKeyDown={onKeyDown}
          onFocus={() => setActive(MINUTES - 1)}
          onBlur={() => setActive(null)}
          onPointerMove={(e) => setActive(barAt(e.clientX))}
          onPointerDown={(e) => setActive(barAt(e.clientX))}
          onPointerLeave={() => setActive(null)}
          className="relative mt-6 h-36 min-w-0 flex-1 touch-pan-y rounded-sm"
        >
          <div className="absolute inset-x-0 top-0 border-t border-border/60" />
          <div className="absolute inset-x-0 top-1/2 border-t border-border/60" />
          <div className="absolute inset-x-0 bottom-0 border-t border-border-strong" />

          <div className="absolute inset-0 flex items-end">
            {bars.map((bar, i) => (
              <div key={bar.minute} className="flex h-full min-w-0 flex-1 items-end justify-center px-px">
                <div
                  style={bar.value > 0 ? { height: `max(3px, ${height(bar.value)}%)` } : undefined}
                  className={cn(
                    "w-full max-w-6 rounded-t transition-colors",
                    bar.value === 0
                      ? "h-0.5 rounded-none bg-surface-hover"
                      : !live
                        ? i === active
                          ? "bg-fg-muted"
                          : "bg-fg-subtle/60"
                        : i === active
                          ? "bg-accent-hover"
                          : i === MINUTES - 1
                            ? "bg-accent/45"
                            : "bg-accent/85",
                  )}
                />
              </div>
            ))}
          </div>

          {hasData && active === null && (
            <span
              aria-hidden
              style={{ left: center(peak), bottom: `calc(${height(bars[peak].value)}% + 5px)`, transform: anchor(peak) }}
              className="pointer-events-none absolute whitespace-nowrap font-mono text-xs tabular-nums text-fg-muted"
            >
              {fmtCompact(bars[peak].value)}
            </span>
          )}

          {active !== null && (
            <div
              aria-hidden
              style={{ left: center(active), bottom: `calc(${height(bars[active].value)}% + 8px)`, transform: anchor(active) }}
              className="pointer-events-none absolute z-10 whitespace-nowrap rounded-md border border-border-strong bg-surface-raised px-2.5 py-1.5 shadow-popover"
            >
              <div className="text-sm font-semibold tabular-nums text-fg">{fmtCommas(bars[active].value)}</div>
              <div className="text-xs text-fg-muted">
                {metric} at {fmtClock(bars[active].minute * 60)}
                {active === MINUTES - 1 && " so far"}
              </div>
            </div>
          )}
        </div>
      </div>

      <div className="relative ml-[46px] mt-2 h-4 font-mono text-xs tabular-nums text-fg-subtle" aria-hidden>
        {bars.map((bar, i) =>
          bar.minute % 5 === 0 ? (
            <span
              key={bar.minute}
              style={{ left: center(i) }}
              className={cn("absolute -translate-x-1/2", bar.minute % 10 !== 0 && "hidden sm:block")}
            >
              {fmtClock(bar.minute * 60)}
            </span>
          ) : null,
        )}
      </div>

      <table className="sr-only">
        <caption>Earnings per minute</caption>
        <thead>
          <tr>
            <th scope="col">Time</th>
            <th scope="col">{metric}</th>
          </tr>
        </thead>
        <tbody>
          {bars
            .filter((bar) => bar.value > 0)
            .map((bar) => (
              <tr key={bar.minute}>
                <td>{fmtClock(bar.minute * 60)}</td>
                <td>{bar.value}</td>
              </tr>
            ))}
        </tbody>
      </table>
    </figure>
  );
}
