import type { FlowPicker, GrinderEvent } from "@/lib/api";

// Everything the Farm page knows about the current run. It's derived purely
// from the event stream: the backend doesn't keep session totals, so these
// numbers live in the browser and only cover what this tab has seen.

export type Metric = "coins" | "tokens";

export type Bucket = Record<Metric, number>;

export interface Session {
  coins: number;
  tokens: number;
  fish: number;
  payouts: number;
  itemCount: number;
  items: Record<string, number>;
  /** Earnings per minute, keyed by unix minute. */
  timeline: Record<number, Bucket>;
  startedAt: number | null;

  activity: string;
  account: string;
  breakUntil: number | null;
  offShift: boolean;
  captcha: boolean;
  gatewayDown: boolean;
  /** The picker the grinder is blocked on. Only an answer or the server clears it. */
  flowPicker: FlowPicker | null;
}

export function freshSession(): Session {
  return {
    coins: 0,
    tokens: 0,
    fish: 0,
    payouts: 0,
    itemCount: 0,
    items: {},
    timeline: {},
    startedAt: null,
    activity: "",
    account: "",
    breakUntil: null,
    offShift: false,
    captcha: false,
    gatewayDown: false,
    flowPicker: null,
  };
}

const TIMELINE_MINUTES = 60;

function earn(s: Session, unix: number, metric: Metric, amount: number) {
  const minute = Math.floor(unix / 60);
  const isNewMinute = !(minute in s.timeline);
  const bucket = s.timeline[minute] ?? { coins: 0, tokens: 0 };
  const timeline = { ...s.timeline, [minute]: { ...bucket, [metric]: bucket[metric] + amount } };
  if (isNewMinute) {
    for (const key of Object.keys(timeline)) {
      if (Number(key) < minute - TIMELINE_MINUTES) delete timeline[Number(key)];
    }
  }
  s.timeline = timeline;
}

function collect(s: Session, items: Record<string, number> | null) {
  if (!items) return 0;
  const next = { ...s.items };
  let total = 0;
  for (const [name, qty] of Object.entries(items)) {
    next[name] = (next[name] ?? 0) + qty;
    total += qty;
  }
  s.items = next;
  s.itemCount += total;
  return total;
}

export function applyEvent(prev: Session, ev: GrinderEvent): Session {
  const s: Session = { ...prev, startedAt: prev.startedAt ?? ev.time };
  switch (ev.kind) {
    case "cmd_success":
      s.coins += ev.reward || 0;
      s.payouts += 1;
      earn(s, ev.time, "coins", ev.reward || 0);
      s.captcha = false;
      break;
    case "item":
      collect(s, ev.items);
      break;
    case "fish_token":
      s.tokens += ev.reward || 0;
      earn(s, ev.time, "tokens", ev.reward || 0);
      s.captcha = false;
      break;
    case "fish_caught":
      s.fish += collect(s, ev.items);
      break;
    case "activity":
      s.activity = ev.activity;
      break;
    case "captcha":
      s.captcha = true;
      break;
    case "break_start":
      s.breakUntil = ev.time + (ev.reward || 0);
      break;
    case "break_end":
      s.breakUntil = null;
      break;
    case "shift_off":
      s.offShift = true;
      break;
    case "shift_on":
      s.offShift = false;
      break;
    case "gateway_up":
      s.gatewayDown = false;
      s.account = ev.message;
      break;
    case "gateway_down":
      s.gatewayDown = true;
      break;
    case "flow_picker":
      s.flowPicker = { message: ev.message, choices: ev.choices ?? [] };
      break;
  }
  return s;
}

// --- feed ---

export type FeedEvent = GrinderEvent & { id: number };

/** Kinds that only matter for live state. The grinder's own run log skips these too. */
export function isFeedEvent(ev: GrinderEvent) {
  return ev.kind !== "activity" && ev.kind !== "cmd_start";
}

// --- persistence ---
// Pages are full navigations, so without this a trip to Settings would zero
// the dashboard. sessionStorage keeps the run alive for the life of the tab.

const SNAPSHOT_KEY = "dmf.farm-session";
const SNAPSHOT_EVENTS = 200;

export interface Snapshot {
  session: Session;
  events: FeedEvent[];
}

export function saveSnapshot({ session, events }: Snapshot) {
  try {
    sessionStorage.setItem(SNAPSHOT_KEY, JSON.stringify({ session, events: events.slice(-SNAPSHOT_EVENTS) }));
  } catch {
    // storage full or blocked: the dashboard just starts blank next time
  }
}

export function loadSnapshot(): Snapshot | null {
  try {
    const raw = sessionStorage.getItem(SNAPSHOT_KEY);
    return raw ? (JSON.parse(raw) as Snapshot) : null;
  } catch {
    return null;
  }
}
