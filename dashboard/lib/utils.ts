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

/** Format a unix seconds timestamp as HH:MM:SS. */
export function fmtTime(unix: number): string {
  const d = new Date(unix * 1000);
  const h = d.getHours().toString().padStart(2, "0");
  const m = d.getMinutes().toString().padStart(2, "0");
  const s = d.getSeconds().toString().padStart(2, "0");
  return `${h}:${m}:${s}`;
}
