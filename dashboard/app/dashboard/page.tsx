"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  FarmSummary,
  GrinderEvent,
  auth,
  connectFarmWS,
  farm,
} from "@/lib/api";
import { cn, fmtCommas, fmtTime } from "@/lib/utils";

// Farm — single-grinder dashboard. One user, one farm, one pair of
// Start/Stop buttons. Stats + inventory + events update live via WS.
export default function Dashboard() {
  const router = useRouter();
  const [me, setMe] = useState<{ email: string; name: string; picture: string } | null>(null);
  const [state, setState] = useState<FarmSummary | null>(null);
  const [events, setEvents] = useState<GrinderEvent[]>([]);
  const [stats, setStats] = useState<Stats>(freshStats());
  const [error, setError] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);

  // Load user + farm summary on mount.
  useEffect(() => {
    (async () => {
      const info = await auth.me();
      if (!info.signed_in) {
        router.replace("/");
        return;
      }
      setMe({
        email: info.user?.email ?? "",
        name: info.user?.name ?? "",
        picture: info.user?.picture ?? "",
      });
      await refresh();
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function refresh() {
    try {
      const s = await farm.summary();
      setState(s);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  // Open WS when running, close when stopped. Reset stats on each start
  // so numbers reflect THIS session, not the lifetime of the farm.
  useEffect(() => {
    if (!state?.is_running) {
      wsRef.current?.close();
      wsRef.current = null;
      return;
    }
    setEvents([]);
    setStats(freshStats());
    const ws = connectFarmWS((ev) => {
      setEvents((prev) => [...prev.slice(-999), ev]);
      setStats((s) => applyEvent(s, ev));
    });
    wsRef.current = ws;
    return () => ws.close();
  }, [state?.is_running]);

  return (
    <div className="flex min-h-dvh">
      <Sidebar page="farm" me={me} />
      <main className="flex-1 overflow-auto p-8">
        <header className="mb-8 flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-semibold">Farm</h1>
            <p className="text-sm text-muted">
              {state?.is_running ? (
                <span className="text-success">● grinding</span>
              ) : (
                <span className="text-muted">● stopped</span>
              )}
            </p>
          </div>
          <div className="flex items-center gap-3">
            {error && <span className="text-sm text-error">{error}</span>}
            {state?.is_running ? (
              <button
                onClick={async () => {
                  try {
                    await farm.stop();
                    await refresh();
                  } catch (e) {
                    setError((e as Error).message);
                  }
                }}
                className="rounded-md border border-border bg-card px-4 py-2 text-sm hover:bg-border/40"
              >
                Stop
              </button>
            ) : (
              <button
                onClick={async () => {
                  setError(null);
                  try {
                    await farm.start();
                    await refresh();
                  } catch (e) {
                    setError((e as Error).message);
                  }
                }}
                disabled={!state?.configured}
                className={cn(
                  "rounded-md px-4 py-2 text-sm font-medium",
                  state?.configured
                    ? "bg-success text-background hover:brightness-110"
                    : "bg-border text-muted cursor-not-allowed",
                )}
                title={state?.configured ? "Start grinder" : "Fill in Discord token/guild/channel in Settings first"}
              >
                Start
              </button>
            )}
          </div>
        </header>

        {!state ? (
          <p className="text-muted">Loading...</p>
        ) : !state.configured ? (
          <div className="rounded-xl border border-warn/40 bg-warn/5 p-6">
            <h2 className="text-lg font-medium text-warn">Set up your farm first</h2>
            <p className="mt-2 text-sm text-muted">
              Head to <a href="/settings/" className="text-accent underline">Settings</a> and fill in your Discord token,
              server ID, and channel ID. Then come back and hit Start.
            </p>
          </div>
        ) : !state.is_running ? (
          <div className="rounded-xl border border-border bg-card p-8 text-muted">
            Farm is stopped. Click <span className="text-foreground">Start</span> to begin grinding.
          </div>
        ) : (
          <div className="grid gap-6">
            <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
              <StatCard label="Balance" value={"+" + fmtCommas(stats.balance)} tone="success" />
              <StatCard label="Tokens" value={fmtCommas(stats.tokens)} tone="success" />
              <StatCard label="Fish" value={fmtCommas(stats.fish)} />
              <StatCard label="Runs" value={fmtCommas(stats.runs)} />
            </div>

            <div className="grid gap-6 md:grid-cols-[1fr_1.4fr]">
              <section className="rounded-xl border border-border bg-card p-5">
                <h2 className="mb-3 text-sm font-medium uppercase text-muted">Inventory</h2>
                {Object.keys(stats.items).length === 0 ? (
                  <p className="text-sm text-muted">(empty)</p>
                ) : (
                  <ul className="scrollbar-thin max-h-80 space-y-1 overflow-auto pr-2 font-mono text-sm">
                    {Object.entries(stats.items)
                      .sort(([a], [b]) => a.localeCompare(b))
                      .map(([name, qty]) => (
                        <li key={name}>
                          <span className="text-success">×{fmtCommas(qty)}</span>{" "}
                          <span className="text-foreground">{name}</span>
                        </li>
                      ))}
                  </ul>
                )}
              </section>

              <section className="rounded-xl border border-border bg-card p-5">
                <h2 className="mb-3 text-sm font-medium uppercase text-muted">Recent Events</h2>
                <ul className="scrollbar-thin h-80 space-y-1 overflow-auto pr-2 font-mono text-sm">
                  {events.length === 0 && <li className="text-muted">(no events yet)</li>}
                  {events
                    .slice()
                    .reverse()
                    .map((ev, i) => (
                      <li key={i} className="flex gap-3">
                        <span className="text-muted">{fmtTime(ev.time)}</span>
                        <EventLine ev={ev} />
                      </li>
                    ))}
                </ul>
              </section>
            </div>
          </div>
        )}
      </main>
    </div>
  );
}

// --- shared sidebar used across pages ---

export function Sidebar({ page, me }: { page: "farm" | "settings"; me: { email: string; picture: string } | null }) {
  return (
    <aside className="flex w-56 flex-col border-r border-border bg-card px-4 py-6">
      <div className="mb-6 flex items-center gap-3">
        <div className="h-6 w-6 rounded bg-accent" />
        <span className="font-semibold">Dank Memer Farm</span>
      </div>
      <nav className="flex flex-col gap-1">
        <NavItem label="Farm" href="/dashboard/" active={page === "farm"} />
        <NavItem label="Settings" href="/settings/" active={page === "settings"} />
      </nav>
      <div className="mt-auto pt-8">
        {me && (
          <div className="flex items-center gap-2 text-xs text-muted">
            {me.picture && <img src={me.picture} alt="" className="h-6 w-6 rounded-full" />}
            <span className="truncate">{me.email}</span>
          </div>
        )}
        <button
          onClick={() => auth.logout().then(() => (location.href = "/"))}
          className="mt-4 text-xs text-muted underline hover:text-foreground"
        >
          Sign out
        </button>
      </div>
    </aside>
  );
}

function NavItem({ label, href, active }: { label: string; href: string; active?: boolean }) {
  return (
    <a
      href={href}
      className={cn(
        "rounded-md px-3 py-2 text-sm",
        active ? "bg-accent/15 text-foreground" : "text-muted hover:bg-border/40 hover:text-foreground",
      )}
    >
      {label}
    </a>
  );
}

function StatCard({ label, value, tone }: { label: string; value: string; tone?: "success" }) {
  return (
    <div className="rounded-xl border border-border bg-card p-5">
      <div className="text-xs font-medium uppercase text-muted">{label}</div>
      <div className={cn("mt-1 font-mono text-2xl", tone === "success" ? "text-success" : "text-foreground")}>
        {value}
      </div>
    </div>
  );
}

function EventLine({ ev }: { ev: GrinderEvent }) {
  const prefix = ev.command ? `[${ev.command}]` : ev.kind.startsWith("break_") ? "[break]" : "";
  switch (ev.kind) {
    case "cmd_success":
      return (
        <span>
          <span className="text-accent">{prefix}</span>{" "}
          <span className="text-success">+{fmtCommas(ev.reward)} coins</span>
        </span>
      );
    case "item":
      return (
        <span>
          <span className="text-accent">[items]</span>{" "}
          <span className="text-success">
            {ev.items &&
              Object.entries(ev.items)
                .map(([n, q]) => `+${q} ${n}`)
                .join(", ")}
          </span>
        </span>
      );
    case "fish_token":
      return (
        <span>
          <span className="text-accent">[tokens]</span>{" "}
          <span className="text-success">+{fmtCommas(ev.reward)}</span>
        </span>
      );
    case "fish_caught":
      return (
        <span>
          <span className="text-accent">[fish]</span>{" "}
          <span className="text-success">
            {ev.items &&
              Object.entries(ev.items)
                .map(([n, q]) => `+${q} ${n}`)
                .join(", ")}
          </span>
        </span>
      );
    case "warn":
      return <span className="text-warn">{ev.message}</span>;
    case "error":
      return <span className="text-error">{ev.message}</span>;
    case "break_start":
    case "break_end":
      return (
        <span>
          <span className="text-accent">[break]</span>{" "}
          <span className="text-warn">{ev.message}</span>
        </span>
      );
    case "captcha":
      return <span className="text-error">CAPTCHA — grinder paused</span>;
    case "gateway_up":
      return <span className="text-success">gateway up ({ev.message})</span>;
    case "gateway_down":
      return <span className="text-warn">gateway reconnecting: {ev.message}</span>;
    default:
      return <span className="text-muted">{ev.message || ev.kind}</span>;
  }
}

interface Stats {
  balance: number;
  tokens: number;
  fish: number;
  runs: number;
  items: Record<string, number>;
}

function freshStats(): Stats {
  return { balance: 0, tokens: 0, fish: 0, runs: 0, items: {} };
}

function applyEvent(s: Stats, ev: GrinderEvent): Stats {
  switch (ev.kind) {
    case "cmd_success":
      return { ...s, balance: s.balance + (ev.reward || 0), runs: s.runs + 1 };
    case "item": {
      const items = { ...s.items };
      if (ev.items) {
        for (const [name, qty] of Object.entries(ev.items)) {
          items[name] = (items[name] || 0) + qty;
        }
      }
      return { ...s, items };
    }
    case "fish_token":
      return { ...s, tokens: s.tokens + (ev.reward || 0) };
    case "fish_caught": {
      const items = { ...s.items };
      let fish = s.fish;
      if (ev.items) {
        for (const [name, qty] of Object.entries(ev.items)) {
          items[name] = (items[name] || 0) + qty;
          fish += qty;
        }
      }
      return { ...s, items, fish };
    }
    default:
      return s;
  }
}
