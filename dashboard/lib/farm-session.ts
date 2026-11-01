import type { FarmSession, GrinderEvent } from "@/lib/api";

// The server owns the run's state. It sends the whole session when a tab
// connects and a fresh copy with every event, so this tab only ever displays
// it: closing the tab loses nothing, and every device shows the same numbers.

export type Session = Omit<FarmSession, "recent">;

export function freshSession(): Session {
  return {
    mode: "",
    currency: "coins",
    started_at: 0,
    ended_at: 0,
    server_time: 0,
    earned: 0,
    payouts: 0,
    fish: 0,
    item_count: 0,
    items: {},
    activity: "",
    account: "",
    break_until: 0,
    off_shift: false,
    captcha: false,
    gateway_down: false,
    discord_up: false,
    token_rejected: false,
    flow_picker: null,
  };
}

/** Kinds that only matter for live state. The grinder's own run log skips these too. */
export function isFeedEvent(ev: GrinderEvent) {
  return ev.kind !== "activity" && ev.kind !== "cmd_start";
}
