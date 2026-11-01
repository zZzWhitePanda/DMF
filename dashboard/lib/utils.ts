import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/** Join tailwind classes with conflict resolution. shadcn convention. */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** Format a number with grouping separators (e.g. 1,234,567). */
export function fmtCommas(n: number): string {
  return n.toLocaleString();
}

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });

/** Format a number for tight spaces (e.g. 12.4K). */
export function fmtCompact(n: number): string {
  return compact.format(n);
}

const pad = (n: number) => n.toString().padStart(2, "0");

/** Format a unix seconds timestamp as HH:MM:SS. */
export function fmtTime(unix: number): string {
  const d = new Date(unix * 1000);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** Format a unix seconds timestamp as HH:MM. */
export function fmtClock(unix: number): string {
  const d = new Date(unix * 1000);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Format a span of seconds as a countdown (e.g. 04:32 or 1:04:32). */
export function fmtCountdown(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  const h = Math.floor(s / 3600);
  const clock = `${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
  return h > 0 ? `${h}:${clock}` : clock;
}

/** Format a span of seconds as a rough duration (e.g. 42m or 3h 12m). */
export function fmtDuration(seconds: number): string {
  const m = Math.floor(seconds / 60);
  if (m < 1) return "under a minute";
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}
