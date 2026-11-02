// Thin wrappers around the backend HTTP + WebSocket API.
//
// Base path is relative in production (same origin as the embedded
// dashboard) and proxied through next.config.js rewrites in dev mode.

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers || {}),
    },
    ...init,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.code || "", body.error || res.statusText);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
  }
}

// ---------------------------------------------------------------------------
// auth
// ---------------------------------------------------------------------------

export type LicenseStatus = "admin" | "ok" | "none" | "inactive" | "hwid_mismatch";

export interface MeResponse {
  signed_in: boolean;
  user?: {
    email: string;
    name: string;
    picture: string;
    is_admin: boolean;
  };
  license_status?: LicenseStatus;
  reset_available_at?: number;
}

export const auth = {
  me: () => request<MeResponse>("/auth/me"),
  signInUrl: (hwid: string) => `/auth/google/start?hwid=${encodeURIComponent(hwid)}`,
  logout: () => request<{ ok: true }>("/auth/logout", { method: "POST" }),
};

// ---------------------------------------------------------------------------
// accounts
// ---------------------------------------------------------------------------

export interface Account {
  id: number;
  name: string;
  is_running: boolean;
  created_at: number;
  updated_at: number;
}

export const accounts = {
  list: () => request<Account[]>("/api/accounts"),
  create: (name: string, config_yaml?: string) =>
    request<Account>("/api/accounts", {
      method: "POST",
      body: JSON.stringify({ name, config_yaml }),
    }),
  get: (id: number) => request<Account>(`/api/accounts/${id}`),
  del: (id: number) => request<{ ok: true }>(`/api/accounts/${id}`, { method: "DELETE" }),
  start: (id: number) => request<{ status: string }>(`/api/accounts/${id}/start`, { method: "POST" }),
  stop: (id: number) => request<{ status: string }>(`/api/accounts/${id}/stop`, { method: "POST" }),
  getConfig: async (id: number): Promise<string> => {
    const res = await fetch(`/api/accounts/${id}/config`, { credentials: "include" });
    if (!res.ok) throw new ApiError(res.status, "", await res.text());
    return res.text();
  },
  saveConfig: (id: number, config_yaml: string) =>
    request<{ ok: true }>(`/api/accounts/${id}/config`, {
      method: "PUT",
      body: JSON.stringify({ config_yaml }),
    }),
};

// ---------------------------------------------------------------------------
// websocket
// ---------------------------------------------------------------------------

export interface GrinderEvent {
  id: number; // orders a run's events
  session: FarmSession | null; // the run's state right after this event
  kind: string;
  command: string;
  message: string;
  reward: number;
  time: number;
  activity: string;
  items: Record<string, number> | null;
  choices: string[] | null;
  outcome: string;
}

export function connectGrinderWS(
  accountId: number,
  onEvent: (ev: GrinderEvent) => void,
  onClose?: () => void,
): WebSocket {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(`${proto}//${location.host}/api/ws/${accountId}`);
  ws.onmessage = (msg) => {
    try {
      onEvent(JSON.parse(msg.data));
    } catch {
      // swallow bad frames
    }
  };
  ws.onclose = () => onClose?.();
  return ws;
}

// ---------------------------------------------------------------------------
// farm — single-account convenience API used by the simplified dashboard.
// Each logged-in user has exactly one farm; the backend auto-creates it
// on first call so the UI never needs an "add account" step.
// ---------------------------------------------------------------------------

export interface FlowPicker {
  message: string; // what Dank Memer showed, for telling the choices apart
  choices: string[];
}

export type Currency = "coins" | "tokens";

/** What a run has done so far. See events.Session on the server. */
export interface FarmSession {
  mode: string;
  currency: Currency; // the one currency this run earns
  started_at: number;
  ended_at: number; // 0 while running
  server_time: number; // when this was taken, for correcting the local clock
  earned: number;
  payouts: number;
  fish: number;
  item_count: number;
  items: Record<string, number>;
  activity: string;
  account: string;
  break_until: number; // 0 unless on a break
  off_shift: boolean;
  captcha: boolean;
  gateway_down: boolean;
  discord_up: boolean;
  token_rejected: boolean;
  flow_picker: FlowPicker | null;
  recent?: GrinderEvent[]; // the feed so far, oldest first
}

export interface FarmSummary {
  name: string;
  is_running: boolean;
  run_id: number; // new on every start, 0 while stopped
  configured: boolean; // Discord token/guild/channel are all filled in
  flow_picker: FlowPicker | null; // set while the grinder waits on a choice
  token_rejected: boolean; // Discord refused this run's token
  mode: string; // what the saved settings would run
  currency: Currency; // and what they would earn
  session: FarmSession | null; // current or last run, null if none since the server started
}

export const farm = {
  summary: (full = false) => request<FarmSummary>(full ? "/api/farm?full" : "/api/farm"),
  getConfig: async (): Promise<string> => {
    const res = await fetch("/api/farm/config", { credentials: "include" });
    if (!res.ok) throw new ApiError(res.status, "", await res.text());
    return res.text();
  },
  saveConfig: (config_yaml: string) =>
    request<{ ok: true }>("/api/farm/config", {
      method: "PUT",
      body: JSON.stringify({ config_yaml }),
    }),
  start: () => request<{ status: string }>("/api/farm/start", { method: "POST" }),
  stop: () => request<{ status: string }>("/api/farm/stop", { method: "POST" }),
  /** Answers the flow picker. -1 cancels. A 409 means nothing is waiting any more. */
  answerFlow: (index: number) =>
    request<{ ok: true }>("/api/farm/flow-choice", {
      method: "POST",
      body: JSON.stringify({ index }),
    }),
};

const WS_NORMAL_CLOSURE = 1000; // the server ending the socket because the farm stopped

export function connectFarmWS(
  onEvent: (ev: GrinderEvent) => void,
  onClose?: (farmStopped: boolean) => void,
): WebSocket {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(`${proto}//${location.host}/api/farm/ws`);
  ws.onmessage = (msg) => {
    try {
      onEvent(JSON.parse(msg.data));
    } catch {
      // swallow bad frames
    }
  };
  ws.onclose = (e) => onClose?.(e.code === WS_NORMAL_CLOSURE);
  return ws;
}
