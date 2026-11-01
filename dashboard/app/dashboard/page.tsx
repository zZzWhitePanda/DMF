"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  Account,
  GrinderEvent,
  accounts as accountsAPI,
  auth,
  connectGrinderWS,
} from "@/lib/api";
import { cn, fmtCommas, fmtTime } from "@/lib/utils";

// Dashboard — live stats + inventory + event log for the currently-
// selected account. Switching accounts via the sidebar swaps the WS.
export default function Dashboard() {
  const router = useRouter();
  const [me, setMe] = useState<{ email: string; name: string; picture: string } | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [events, setEvents] = useState<GrinderEvent[]>([]);
  const [stats, setStats] = useState<Stats>(freshStats());
  const wsRef = useRef<WebSocket | null>(null);

  // Bootstrap: load profile + account list.
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
      const list = await accountsAPI.list();
      setAccounts(list);
      if (list.length > 0) setSelectedID(list[0].id);
    })();
  }, [router]);

  // WebSocket lifecycle — reconnect on account switch.
  useEffect(() => {
    if (!selectedID) return;
    const acc = accounts.find((a) => a.id === selectedID);
    if (!acc?.is_running) return;
    setEvents([]);
    setStats(freshStats());
    const ws = connectGrinderWS(selectedID, (ev) => {
      setEvents((prev) => [...prev.slice(-999), ev]);
      setStats((s) => applyEvent(s, ev));
    });
    wsRef.current = ws;
    return () => ws.close();
  }, [selectedID, accounts]);

  const selected = accounts.find((a) => a.id === selectedID) ?? null;

  return (
    <div className="flex min-h-dvh">
      <aside className="w-64 border-r border-border bg-card px-4 py-6">
        <div className="mb-6 flex items-center gap-3">
          <div className="h-6 w-6 rounded bg-accent" />
          <span className="font-semibold">Dank Memer Farm</span>
        </div>
        <nav className="flex flex-col gap-1">
          <NavItem label="Dashboard" active />
          <NavItem label="Settings" href="/settings/" />
          <NavItem label="Logs" href="/logs/" />
        </nav>
        <div className="mt-8">
          <div className="mb-2 text-xs font-medium uppercase text-muted">Accounts</div>
          <ul className="flex flex-col gap-1">
            {accounts.map((a) => (
              <li key={a.id}>
                <button
                  onClick={() => setSelectedID(a.id)}
                  className={cn(
                    "flex w-full items-center justify-between rounded-md px-3 py-2 text-sm",
                    a.id === selectedID
                      ? "bg-accent/15 text-foreground"
                      : "text-muted hover:bg-border/40 hover:text-foreground",
                  )}
                >
                  <span>{a.name}</span>
                  <span className={cn("h-2 w-2 rounded-full", a.is_running ? "bg-success" : "bg-muted")} />
                </button>
              </li>
            ))}
            <li>
              <NewAccountButton
                onCreated={(a) => {
                  setAccounts((prev) => [a, ...prev]);
                  setSelectedID(a.id);
                }}
              />
            </li>
          </ul>
        </div>
        <div className="mt-auto pt-8">
          {me && (
            <div className="flex items-center gap-2 text-xs text-muted">
              {me.picture && <img src={me.picture} alt="" className="h-6 w-6 rounded-full" />}
              <span className="truncate">{me.email}</span>
            </div>
          )}
        </div>
      </aside>

      <main className="flex-1 overflow-auto p-8">
        <header className="mb-8 flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-semibold">
              {selected?.name ?? "No account"}
            </h1>
            <p className="text-sm text-muted">
              {selected?.is_running ? "grinding" : "stopped"}
            </p>
          </div>
          {selected && (
            <div className="flex gap-2">
              {selected.is_running ? (
                <button
                  onClick={async () => {
                    await accountsAPI.stop(selected.id);
                    refreshAccounts(setAccounts);
                  }}
                  className="rounded-md border border-border bg-card px-4 py-2 text-sm hover:bg-border/40"
                >
                  Stop
                </button>
              ) : (
                <button
                  onClick={async () => {
                    try {
                      await accountsAPI.start(selected.id);
                      refreshAccounts(setAccounts);
                    } catch (e: unknown) {
                      alert((e as Error).message);
                    }
                  }}
                  className="rounded-md bg-success px-4 py-2 text-sm font-medium text-background hover:brightness-110"
                >
                  Start
                </button>
              )}
            </div>
          )}
        </header>

        {!selected ? (
          <EmptyState />
        ) : !selected.is_running ? (
          <div className="rounded-xl border border-border bg-card p-8 text-muted">
            Grinder is stopped. Click <span className="text-foreground">Start</span> to begin.
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

function NavItem({ label, href, active }: { label: string; href?: string; active?: boolean }) {
  const cls = cn(
    "block rounded-md px-3 py-2 text-sm",
    active ? "bg-accent/15 text-foreground" : "text-muted hover:bg-border/40 hover:text-foreground",
  );
  return href ? (
    <a href={href} className={cls}>
      {label}
    </a>
  ) : (
    <span className={cls}>{label}</span>
  );
}

function NewAccountButton({ onCreated }: { onCreated: (a: Account) => void }) {
  return (
    <button
      onClick={async () => {
        const name = window.prompt("Name this account:");
        if (!name) return;
        try {
          const acc = await accountsAPI.create(name);
          onCreated(acc);
        } catch (e: unknown) {
          alert((e as Error).message);
        }
      }}
      className="mt-2 rounded-md border border-dashed border-border px-3 py-2 text-sm text-muted hover:border-muted hover:text-foreground"
    >
      + Add account
    </button>
  );
}

function EmptyState() {
  return (
    <div className="rounded-xl border border-border bg-card p-10 text-center">
      <h2 className="text-lg font-medium">No accounts yet</h2>
      <p className="mt-2 text-sm text-muted">Create one from the sidebar.</p>
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

async function refreshAccounts(setter: (next: Account[]) => void) {
  const list = await accountsAPI.list();
  setter(list);
}

// ---------------------------------------------------------------------------
// derived stats
// ---------------------------------------------------------------------------

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
    case "cmd_success": {
      return { ...s, balance: s.balance + (ev.reward || 0), runs: s.runs + 1 };
    }
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
