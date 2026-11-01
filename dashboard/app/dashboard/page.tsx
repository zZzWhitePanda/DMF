"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowRight, KeyRound, Play, RotateCw, ShieldAlert, Square, TriangleAlert, Waypoints } from "lucide-react";
import { ApiError, FarmSummary, FlowPicker, GrinderEvent, connectFarmWS, farm } from "@/lib/api";
import {
  FeedEvent,
  Metric,
  Session,
  applyEvent,
  freshSession,
  isFeedEvent,
  loadSnapshot,
  saveSnapshot,
} from "@/lib/farm-session";
import { useUser } from "@/lib/use-user";
import { cn, fmtCommas, fmtCompact, fmtCountdown, fmtDuration } from "@/lib/utils";
import { ActivityFeed } from "@/components/activity-feed";
import { AppShell } from "@/components/app-shell";
import { EarningsChart } from "@/components/earnings-chart";
import {
  Banner,
  Button,
  Panel,
  PanelHeader,
  Segmented,
  ServerError,
  Skeleton,
  StatusDot,
  Tone,
  buttonStyles,
} from "@/components/ui";

const MAX_EVENTS = 1000;
const MAX_RETRY_MS = 15_000;
const STOPPED_POLL_MS = 3_000;
/** Longest formatted total that still fits a phone at the full hero size. */
const HERO_FULL_SIZE_CHARS = 13;

type Connection = "idle" | "connecting" | "live" | "lost";
type Action = "start" | "stop" | "restart";
type FarmError = { title: string; message: string };

interface Status {
  label: string;
  tone: Tone;
  pulse?: boolean;
  detail?: string;
}

function deriveStatus(summary: FarmSummary, session: Session, connection: Connection, now: number): Status {
  if (!summary.is_running) return { label: "Stopped", tone: "neutral" };
  if (connection === "lost") {
    return { label: "Connection lost", tone: "warn", detail: "Retrying. The farm keeps running on the server." };
  }
  if (session.tokenRejected) return { label: "Token rejected", tone: "danger" };
  if (session.captcha) return { label: "Captcha", tone: "danger" };
  if (session.flowPicker) return { label: "Waiting on a choice", tone: "warn" };
  if (session.gatewayDown) return { label: "Reconnecting", tone: "warn", detail: "Lost the Discord gateway" };
  if (session.breakUntil && session.breakUntil > now) {
    return { label: "On break", tone: "warn", detail: `Back in ${fmtCountdown(session.breakUntil - now)}` };
  }
  if (session.offShift) return { label: "Off shift", tone: "warn", detail: "Back when the next shift starts" };
  if (connection === "connecting" || !session.discordUp) return { label: "Connecting to Discord", tone: "neutral" };
  return {
    label: "Grinding",
    tone: "live",
    pulse: true,
    detail: session.activity ? `Running ${session.activity}` : undefined,
  };
}

function useNow() {
  const [now, setNow] = useState(0);
  useEffect(() => {
    const tick = () => setNow(Math.floor(Date.now() / 1000));
    tick();
    const t = setInterval(tick, 1000);
    return () => clearInterval(t);
  }, []);
  return now;
}

// Farm — single-grinder dashboard. One user, one farm, one Start/Stop.
// Everything below the status line is fed live by the WebSocket.
export default function Dashboard() {
  const { user, failed, retry } = useUser();
  const [summary, setSummary] = useState<FarmSummary | null>(null);
  const [session, setSession] = useState<Session>(freshSession);
  const [events, setEvents] = useState<FeedEvent[]>([]);
  const [connection, setConnection] = useState<Connection>("idle");
  const [busy, setBusy] = useState<Action | null>(null);
  const [answering, setAnswering] = useState<number | null>(null);
  const [error, setError] = useState<FarmError | null>(null);
  const [picked, setPicked] = useState<Metric | null>(null);
  const now = useNow();

  const nextId = useRef(0);
  const lastRun = useRef<number | null>(null);
  const latest = useRef({ session, events });
  latest.current = { session, events };

  const loadSummary = useCallback(async () => {
    const next = await farm.summary();
    setSummary(next);
    // The server's word beats the stored session: this tab may have missed
    // the picker's one event, or kept one that's since been answered.
    setSession((s) => ({ ...s, flowPicker: next.flow_picker }));
  }, []);

  const refresh = useCallback(async () => {
    try {
      await loadSummary();
    } catch (e) {
      setError({ title: "Couldn't reach the server", message: (e as Error).message });
    }
  }, [loadSummary]);

  useEffect(() => {
    if (user) refresh();
  }, [user, refresh]);

  // Carry the run across page loads for as long as this tab lives.
  useEffect(() => {
    const snapshot = loadSnapshot();
    if (snapshot) {
      setSession({ ...freshSession(), ...snapshot.session });
      setEvents(snapshot.events);
      nextId.current = (snapshot.events.at(-1)?.id ?? -1) + 1;
    }
    const save = () => saveSnapshot(latest.current);
    window.addEventListener("pagehide", save);
    return () => window.removeEventListener("pagehide", save);
  }, []);

  const reset = useCallback(() => {
    setSession(freshSession());
    setEvents([]);
  }, []);

  const isRunning = !!summary?.is_running;
  const runId = summary?.run_id ?? 0;
  const loaded = summary !== null;

  // Stay connected for as long as this run lasts, retrying with backoff if the
  // socket drops. The server ends every socket when the farm stops, so re-check
  // on every close. Keyed on the run, not is_running: a restart can go from one
  // run to the next without this tab ever seeing the farm stopped.
  useEffect(() => {
    if (!loaded) return;
    const before = lastRun.current;
    lastRun.current = runId;
    if (!runId) {
      setConnection("idle");
      return;
    }
    if (before !== null && before !== runId) reset();

    let cancelled = false;
    let attempts = 0;
    let retryTimer: ReturnType<typeof setTimeout>;
    let ws: WebSocket;

    const onEvent = (ev: GrinderEvent) => {
      setSession((s) => applyEvent(s, ev));
      if (isFeedEvent(ev)) {
        const row = { ...ev, id: nextId.current++ };
        setEvents((prev) => [...prev.slice(-(MAX_EVENTS - 1)), row]);
      }
    };

    const connect = () => {
      setConnection((c) => (c === "lost" ? c : "connecting"));
      ws = connectFarmWS(onEvent, (farmStopped) => {
        if (cancelled) return;
        if (!farmStopped) setConnection("lost");
        refresh();
        retryTimer = setTimeout(connect, Math.min(1000 * 2 ** attempts++, MAX_RETRY_MS));
      });
      ws.addEventListener("open", () => {
        attempts = 0;
        setConnection("live");
      });
    };
    connect();

    return () => {
      cancelled = true;
      clearTimeout(retryTimer);
      ws.close();
    };
  }, [loaded, runId, refresh, reset]);

  // Nothing pushes to a page with no socket, so look out for a run started
  // elsewhere, such as another tab's Start or Restart. Failures stay quiet:
  // nobody asked for this, and the next poll tries again.
  useEffect(() => {
    if (!loaded || runId) return;
    const t = setInterval(() => loadSummary().catch(() => {}), STOPPED_POLL_MS);
    return () => clearInterval(t);
  }, [loaded, runId, loadSummary]);

  async function run(action: Action, fn: () => Promise<unknown>) {
    setBusy(action);
    setError(null);
    try {
      await fn();
      await refresh();
    } catch (e) {
      setError({ title: `Couldn't ${action} the farm`, message: (e as Error).message });
    } finally {
      setBusy(null);
    }
  }

  const start = () => run("start", farm.start);
  const stop = () => run("stop", farm.stop);
  const restart = () =>
    run("restart", async () => {
      await farm.stop();
      reset();
      await farm.start();
    });

  async function answerFlow(index: number) {
    setAnswering(index);
    setError(null);
    try {
      await farm.answerFlow(index);
      setSession((s) => ({ ...s, flowPicker: null }));
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        // Already answered, by a second click or another tab.
        setSession((s) => ({ ...s, flowPicker: null }));
      } else {
        setError({ title: "Couldn't send your choice", message: (e as Error).message });
        await refresh();
      }
    } finally {
      setAnswering(null);
    }
  }

  const status = summary ? deriveStatus(summary, session, connection, now) : null;

  useEffect(() => {
    document.title = status && isRunning ? `${status.label} · Dank Memer Farm` : "Dank Memer Farm";
  }, [status?.label, isRunning]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <AppShell page="farm" user={user}>
      {failed ? (
        <ServerError onRetry={retry} />
      ) : !summary ? (
        error ? (
          <ServerError onRetry={refresh} message={error.message} />
        ) : (
          <FarmSkeleton />
        )
      ) : !summary.configured ? (
        <SetupCard />
      ) : (
        <div className="grid gap-4 sm:gap-5">
          <Panel className="overflow-hidden">
            <div className="flex min-h-[60px] items-center justify-between gap-3 border-b border-border px-4 py-2.5 sm:px-5">
              <StatusLine status={status!} account={session.account} />
              {isRunning ? (
                <Button variant="secondary" size="sm" loading={busy === "stop"} disabled={!!busy} onClick={stop}>
                  {busy !== "stop" && <Square />}
                  Stop farm
                </Button>
              ) : (
                <Button loading={busy === "start"} disabled={!!busy} onClick={start}>
                  {busy !== "start" && <Play />}
                  Start farm
                </Button>
              )}
            </div>

            {error && (
              <Banner
                tone="danger"
                icon={<TriangleAlert />}
                title={error.title}
                className="border-b border-border"
                action={
                  <Button variant="ghost" size="sm" onClick={() => setError(null)}>
                    Dismiss
                  </Button>
                }
              >
                {error.message}
              </Banner>
            )}
            {isRunning && session.tokenRejected && (
              <Banner
                tone="danger"
                icon={<KeyRound />}
                title="Discord rejected your token"
                className="border-b border-border"
                action={
                  <a href="/settings/#discord" className={buttonStyles({ variant: "danger", size: "sm" })}>
                    Update token
                  </a>
                }
              >
                The farm can't log in, so nothing is running. Save a fresh token in Settings, then stop and start the
                farm.
              </Banner>
            )}
            {isRunning && session.captcha && (
              <Banner
                tone="danger"
                icon={<ShieldAlert />}
                title="Dank Memer sent a captcha"
                className="border-b border-border"
                action={
                  <Button variant="danger" size="sm" loading={busy === "restart"} disabled={!!busy} onClick={restart}>
                    {busy !== "restart" && <RotateCw />}
                    Restart farm
                  </Button>
                }
              >
                Solve it in Discord, then restart the farm to pick up where it left off.
              </Banner>
            )}
            {isRunning && session.flowPicker && (
              <FlowPickerBanner picker={session.flowPicker} answering={answering} onAnswer={answerFlow} />
            )}

            <Earnings session={session} running={isRunning} now={now} picked={picked} onPick={setPicked} />
          </Panel>

          <div className="grid gap-4 sm:gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
            <ActivityFeed events={events} running={isRunning} />
            <Inventory items={session.items} total={session.itemCount} />
          </div>
        </div>
      )}
    </AppShell>
  );
}

function StatusLine({ status, account }: { status: Status; account: string }) {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-0.5" role="status">
      <StatusDot tone={status.tone} pulse={status.pulse} />
      <h1 className="text-[15px] font-semibold tracking-tight text-fg">{status.label}</h1>
      {status.detail && <span className="text-[13px] tabular-nums text-fg-muted">{status.detail}</span>}
      {account && status.tone !== "neutral" && (
        <span className="hidden font-mono text-xs text-fg-subtle sm:inline">{account}</span>
      )}
    </div>
  );
}

/** Dank Memer often labels every flow "View", so number any that repeat. */
function choiceLabels(choices: string[]) {
  return choices.map((c, i) => (choices.indexOf(c) === choices.lastIndexOf(c) ? c : `${c} ${i + 1}`));
}

function FlowPickerBanner({
  picker,
  answering,
  onAnswer,
}: {
  picker: FlowPicker;
  answering: number | null;
  onAnswer: (index: number) => void;
}) {
  return (
    <Banner
      tone="warn"
      icon={<Waypoints />}
      title="Flow is waiting for a choice"
      className="border-b border-border sm:flex-col sm:items-stretch sm:gap-3"
      action={
        <div className="flex flex-wrap gap-2 sm:pl-7">
          {choiceLabels(picker.choices).map((label, i) => (
            <Button
              key={i}
              variant="secondary"
              size="sm"
              title={label}
              className="max-w-full"
              loading={answering === i}
              disabled={answering !== null}
              onClick={() => onAnswer(i)}
            >
              <span className="truncate">{label}</span>
            </Button>
          ))}
        </div>
      }
    >
      <span className="scrollbar-thin block max-h-32 overflow-y-auto whitespace-pre-line">
        {picker.message || "Pick a flow to start earning."}
      </span>
    </Banner>
  );
}

function Earnings({
  session,
  running,
  now,
  picked,
  onPick,
}: {
  session: Session;
  running: boolean;
  now: number;
  picked: Metric | null;
  onPick: (m: Metric) => void;
}) {
  // Fish mode only earns tokens, so lead with whatever is actually moving.
  const metric = picked ?? (session.coins === 0 && session.tokens > 0 ? "tokens" : "coins");
  const other: Metric = metric === "coins" ? "tokens" : "coins";
  const value = session[metric];
  const formatted = fmtCommas(value);
  const elapsed = session.startedAt ? now - session.startedAt : 0;
  const hasRun = session.startedAt !== null;
  const showRate = running && elapsed >= 60;

  return (
    <>
      <div className="px-4 pt-5 sm:px-5 sm:pt-6">
        <p
          className={cn(
            "whitespace-nowrap font-semibold tracking-[-0.03em]",
            formatted.length > HERO_FULL_SIZE_CHARS ? "text-[32px]" : "text-[44px]",
            "leading-none sm:text-[64px] sm:leading-none",
            value === 0 && "text-fg-subtle",
          )}
        >
          {formatted}
        </p>
        <div className="mt-2.5 flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
          <p className="text-sm text-fg-muted">
            {metric}{" "}
            <span className={cn(showRate && "hidden sm:inline")}>
              {running || !hasRun ? "this session" : "last session"}
            </span>
            {showRate && (
              <span className="text-fg-subtle">
                {" · "}
                {fmtCompact(Math.round((value / elapsed) * 3600))}/hr
                <span className="hidden sm:inline"> over {fmtDuration(elapsed)}</span>
              </span>
            )}
          </p>
          <Segmented
            label="Show earnings in"
            value={metric}
            onChange={onPick}
            options={[
              { value: "coins", label: "Coins" },
              { value: "tokens", label: "Tokens" },
            ]}
          />
        </div>
        <div className="mt-1">
          <EarningsChart timeline={session.timeline} metric={metric} now={now} live={running} />
        </div>
      </div>

      <dl className="mt-4 grid grid-cols-2 gap-px border-t border-border bg-border sm:grid-cols-4">
        <Stat label={other === "tokens" ? "Fish tokens" : "Coins"} value={session[other]} />
        <Stat label="Fish caught" value={session.fish} />
        <Stat label="Items found" value={session.itemCount} />
        <Stat label="Payouts" value={session.payouts} />
      </dl>
    </>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="bg-surface px-4 py-3.5 sm:px-5">
      <dt className="text-[13px] text-fg-muted">{label}</dt>
      <dd className={cn("mt-1 text-xl font-semibold tracking-tight", value === 0 ? "text-fg-subtle" : "text-fg")}>
        {fmtCommas(value)}
      </dd>
    </div>
  );
}

function Inventory({ items, total }: { items: Record<string, number>; total: number }) {
  const rows = useMemo(
    () => Object.entries(items).sort(([a, qa], [b, qb]) => qb - qa || a.localeCompare(b)),
    [items],
  );
  const most = rows[0]?.[1] ?? 1;

  return (
    <Panel className="flex h-[440px] min-w-0 flex-col">
      <PanelHeader
        title="Inventory"
        action={
          total > 0 ? (
            <span className="text-[13px] tabular-nums text-fg-muted">
              {fmtCommas(total)} from {rows.length} {rows.length === 1 ? "type" : "types"}
            </span>
          ) : undefined
        }
      />
      {rows.length === 0 ? (
        <p className="flex flex-1 items-center justify-center px-6 text-center text-[13px] text-fg-muted">
          Items the farm picks up stack here.
        </p>
      ) : (
        <ul className="scrollbar-thin min-h-0 flex-1 overflow-y-auto py-1.5">
          {rows.map(([name, qty]) => (
            <li key={name} className="px-4 py-1.5 sm:px-5">
              <div className="flex items-baseline justify-between gap-3 text-[13px] leading-5">
                <span className="min-w-0 truncate text-fg" title={name}>
                  {name}
                </span>
                <span className="font-mono tabular-nums text-fg-muted">{fmtCommas(qty)}</span>
              </div>
              <div className="mt-1 h-0.5 rounded-full bg-surface-hover">
                <div className="h-full rounded-full bg-fg-subtle" style={{ width: `${(qty / most) * 100}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function SetupCard() {
  const steps = [
    { title: "Add your Discord token", body: "Stored on the server and used to run commands." },
    { title: "Pick a server and channel", body: "This is where the farm runs its commands." },
    { title: "Start the farm", body: "It keeps running after you close this page." },
  ];
  return (
    <Panel className="mx-auto mt-[6dvh] max-w-lg">
      <div className="p-6 sm:p-8">
        <h1 className="text-2xl font-semibold tracking-tight">Set up your farm</h1>
        <p className="mt-1.5 text-sm text-fg-muted">Three steps and it's grinding.</p>
        <ol className="mt-6 grid gap-5">
          {steps.map((step, i) => (
            <li key={step.title} className="flex gap-3.5">
              <span className="flex size-6 shrink-0 items-center justify-center rounded-full border border-border-strong font-mono text-xs text-fg-muted">
                {i + 1}
              </span>
              <div>
                <p className="text-sm font-medium text-fg">{step.title}</p>
                <p className="mt-0.5 text-[13px] text-fg-muted">{step.body}</p>
              </div>
            </li>
          ))}
        </ol>
        <a href="/settings/" className={buttonStyles({ className: "mt-7 w-full sm:w-auto" })}>
          Open settings
          <ArrowRight />
        </a>
      </div>
    </Panel>
  );
}

function FarmSkeleton() {
  return (
    <div className="grid gap-4 sm:gap-5" aria-busy="true" aria-label="Loading farm">
      <Panel className="overflow-hidden">
        <div className="flex h-[60px] items-center justify-between border-b border-border px-4 sm:px-5">
          <Skeleton className="h-5 w-32" />
          <Skeleton className="h-9 w-28" />
        </div>
        <div className="p-4 sm:p-5">
          <Skeleton className="h-12 w-64 max-w-full sm:h-16" />
          <Skeleton className="mt-3 h-4 w-40" />
          <Skeleton className="mt-8 h-40" />
        </div>
      </Panel>
      <div className="grid gap-4 sm:gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
        <Skeleton className="h-[440px] rounded-lg" />
        <Skeleton className="hidden h-[440px] rounded-lg lg:block" />
      </div>
    </div>
  );
}
