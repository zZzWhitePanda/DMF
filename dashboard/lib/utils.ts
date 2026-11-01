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

/** Format a unix seconds timestamp as a 12-hour time (e.g. 2:05:09 PM). */
export function fmtTime(unix: number): string {
  const d = new Date(unix * 1000);
  const hours = d.getHours();
  return `${hours % 12 || 12}:${pad(d.getMinutes())}:${pad(d.getSeconds())} ${hours < 12 ? "AM" : "PM"}`;
}

/** Format a span of seconds as a countdown (e.g. 04:32 or 1:04:32). */
export function fmtCountdown(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  const h = Math.floor(s / 3600);
  const clock = `${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
  return h > 0 ? `${h}:${clock}` : clock;
}

/** Format a running time as HH:MM:SS. Hours keep counting past 99. */
export function fmtElapsed(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  return `${pad(Math.floor(s / 3600))}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
}
