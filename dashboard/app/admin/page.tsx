"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Check, Copy, KeyRound, TriangleAlert, UserPlus } from "lucide-react";
import { AdminRow, KeyRow, admin } from "@/lib/api";
import { useUser } from "@/lib/use-user";
import { cn, fmtCommas } from "@/lib/utils";
import { AppShell } from "@/components/app-shell";
import { Banner, Button, Panel, PanelHeader, ServerError, Skeleton, TextField, Tone, TONE_TEXT } from "@/components/ui";

// Keeps every admin's screen on the same list without a manual refresh.
const POLL_MS = 10_000;

const money = (cents: number) => `A$${(cents / 100).toFixed(2)}`;
const day = (unix: number) => new Date(unix * 1000).toLocaleDateString();

function keyStatus(k: KeyRow): { label: string; tone: Tone } {
  if (k.revoked_at) return { label: "Revoked", tone: "danger" };
  if (k.expires_at && k.expires_at * 1000 < Date.now()) return { label: "Expired", tone: "warn" };
  if (k.email) return { label: "In use", tone: "live" };
  return { label: "Unused", tone: "neutral" };
}

function useCopy() {
  const [copied, setCopied] = useState("");
  const copy = useCallback((text: string) => {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(text);
      setTimeout(() => setCopied(""), 1500);
    });
  }, []);
  return { copied, copy };
}

// Admin console. Keys and admins live in the server's database, so every admin
// sees the same data. The server enforces all of this: hiding the tab and
// redirecting here are only for tidiness.
export default function AdminPage() {
  const router = useRouter();
  const { user, failed, retry } = useUser();
  const [keys, setKeys] = useState<KeyRow[] | null>(null);
  const [admins, setAdmins] = useState<AdminRow[]>([]);
  const [owner, setOwner] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);

  const isAdmin = !!user?.isAdmin;

  const load = useCallback(async () => {
    try {
      const [k, a] = await Promise.all([admin.keys(), admin.admins()]);
      setKeys(k.keys);
      setAdmins(a.admins);
      setOwner(a.owner);
      setLoadFailed(false);
    } catch {
      setLoadFailed(true);
    }
  }, []);

  useEffect(() => {
    if (user && !user.isAdmin) router.replace("/dashboard/");
  }, [user, router]);

  useEffect(() => {
    if (!isAdmin) return;
    load();
    const t = setInterval(load, POLL_MS);
    return () => clearInterval(t);
  }, [isAdmin, load]);

  /** Runs a change, then reloads so the screen shows what the server now has. */
  const act = useCallback(
    async (fn: () => Promise<unknown>) => {
      setError(null);
      try {
        await fn();
        await load();
        return true;
      } catch (e) {
        setError((e as Error).message);
        return false;
      }
    },
    [load],
  );

  if (user && !user.isAdmin) return null;

  return (
    <AppShell page="admin" user={user}>
      {failed || (loadFailed && !keys) ? (
        <ServerError onRetry={failed ? retry : load} />
      ) : (
        <div className="grid gap-4 sm:gap-5">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">Admin</h1>
            <p className="mt-1 text-sm text-fg-muted">Every admin sees the same keys. This updates by itself.</p>
          </div>

          {error && (
            <Panel className="overflow-hidden">
              <Banner
                tone="danger"
                icon={<TriangleAlert />}
                title="That didn't go through"
                action={
                  <Button variant="ghost" size="sm" onClick={() => setError(null)}>
                    Dismiss
                  </Button>
                }
              >
                {error}
              </Banner>
            </Panel>
          )}

          {keys ? <Totals keys={keys} /> : <Skeleton className="h-[74px] rounded-lg" />}
          <MintPanel act={act} />
          <KeysPanel keys={keys} act={act} />
          <AdminsPanel admins={admins} owner={owner} isOwner={!!user?.isOwner} act={act} />
        </div>
      )}
    </AppShell>
  );
}

function Totals({ keys }: { keys: KeyRow[] }) {
  const inUse = keys.filter((k) => keyStatus(k).label === "In use").length;
  const unused = keys.filter((k) => keyStatus(k).label === "Unused").length;
  const paid = keys.reduce((sum, k) => sum + k.paid_cents, 0);
  return (
    <Panel className="overflow-hidden">
      <dl className="grid grid-cols-2 gap-px bg-border sm:grid-cols-4">
        {[
          ["Keys", fmtCommas(keys.length)],
          ["In use", fmtCommas(inUse)],
          ["Unused", fmtCommas(unused)],
          ["Total paid", money(paid)],
        ].map(([label, value]) => (
          <div key={label} className="bg-surface px-4 py-3.5 sm:px-5">
            <dt className="text-[13px] text-fg-muted">{label}</dt>
            <dd className="mt-1 text-xl font-semibold tabular-nums tracking-tight text-fg">{value}</dd>
          </div>
        ))}
      </dl>
    </Panel>
  );
}

function MintPanel({ act }: { act: (fn: () => Promise<unknown>) => Promise<boolean> }) {
  const [count, setCount] = useState("1");
  const [paid, setPaid] = useState("");
  const [days, setDays] = useState("");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [minted, setMinted] = useState<string[]>([]);
  const { copied, copy } = useCopy();

  async function mint() {
    setBusy(true);
    const ok = await act(async () => {
      const res = await admin.mint({
        count: Number(count),
        paid_cents: Math.round((parseFloat(paid) || 0) * 100),
        notes,
        expires_days: Number(days) || 0,
      });
      setMinted(res.keys);
    });
    if (ok) {
      setNotes("");
      setPaid("");
    }
    setBusy(false);
  }

  const all = minted.join("\n");

  return (
    <Panel>
      <PanelHeader title="Mint keys" />
      <div className="grid gap-4 p-4 sm:p-5">
        <div className="grid gap-4 sm:grid-cols-3">
          <TextField label="Count (1 to 500)" type="number" inputMode="numeric" value={count} onChange={setCount} />
          <TextField label="Total paid (AUD, optional)" inputMode="decimal" placeholder="45.00" value={paid} onChange={setPaid} />
          <TextField
            label="Expires after days (optional)"
            type="number"
            inputMode="numeric"
            placeholder="Never"
            value={days}
            onChange={setDays}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <label htmlFor="mint-notes" className="text-sm font-medium text-fg">
            Notes (buyer, payment, referrer)
          </label>
          <textarea
            id="mint-notes"
            rows={2}
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            className="w-full resize-y rounded-md border border-border bg-bg px-3 py-2 text-base text-fg outline-none transition-colors placeholder:text-fg-subtle hover:border-border-strong focus:border-accent focus:ring-1 focus:ring-accent sm:text-sm"
          />
        </div>
        <Button loading={busy} onClick={mint}>
          {!busy && <KeyRound />}
          Mint
        </Button>

        {minted.length > 0 && (
          <div className="rounded-md border border-border bg-bg p-3">
            <div className="mb-2 flex items-center justify-between gap-3">
              <p className="text-sm font-medium text-fg">
                {minted.length === 1 ? "New key" : `${minted.length} new keys`}
              </p>
              <Button variant="secondary" size="sm" onClick={() => copy(all)}>
                {copied === all ? <Check /> : <Copy />}
                {copied === all ? "Copied" : "Copy"}
              </Button>
            </div>
            <pre className="scrollbar-thin max-h-40 overflow-auto font-mono text-[13px] leading-6 text-fg">{all}</pre>
          </div>
        )}
      </div>
    </Panel>
  );
}

function KeysPanel({
  keys,
  act,
}: {
  keys: KeyRow[] | null;
  act: (fn: () => Promise<unknown>) => Promise<boolean>;
}) {
  const [search, setSearch] = useState("");
  const { copied, copy } = useCopy();

  const rows = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!keys || !q) return keys ?? [];
    return keys.filter((k) => [k.key, k.email, k.notes, k.created_by].some((v) => v.toLowerCase().includes(q)));
  }, [keys, search]);

  return (
    <Panel className="min-w-0">
      <PanelHeader
        title="Keys"
        action={
          <input
            aria-label="Search keys"
            placeholder="Search"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-9 w-36 rounded-md border border-border bg-bg px-3 text-base text-fg outline-none placeholder:text-fg-subtle focus:border-accent sm:h-8 sm:w-56 sm:text-sm"
          />
        }
      />
      {!keys ? (
        <div className="p-4 sm:p-5">
          <Skeleton className="h-24" />
        </div>
      ) : rows.length === 0 ? (
        <p className="px-6 py-10 text-center text-[13px] text-fg-muted">
          {keys.length === 0 ? "Keys you mint show up here." : "No keys match that."}
        </p>
      ) : (
        <ul className="divide-y divide-border">
          {rows.map((k) => {
            const status = keyStatus(k);
            return (
              <li key={k.key} className="flex flex-col gap-2 px-4 py-3 sm:px-5 lg:flex-row lg:items-center lg:gap-4">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                    <button
                      onClick={() => copy(k.key)}
                      title="Copy key"
                      className="inline-flex cursor-pointer items-center gap-1.5 rounded-sm font-mono text-[13px] text-fg transition-colors hover:text-fg-muted"
                    >
                      {k.key}
                      {copied === k.key ? <Check className="size-3.5" /> : <Copy className="size-3.5 text-fg-subtle" />}
                    </button>
                    <span className={cn("text-[13px] font-medium", TONE_TEXT[status.tone])}>{status.label}</span>
                  </div>
                  <p className="mt-1 break-words text-[13px] text-fg-muted">
                    {[
                      k.email,
                      k.notes,
                      k.paid_cents ? money(k.paid_cents) : "",
                      `made ${day(k.created_at)}`,
                      k.created_by ? `by ${k.created_by}` : "",
                      k.expires_at ? `expires ${day(k.expires_at)}` : "",
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                </div>
                <div className="flex shrink-0 flex-wrap gap-2">
                  {k.device_set && (
                    <Button variant="secondary" size="sm" onClick={() => act(() => admin.resetDevice(k.key))}>
                      Reset device
                    </Button>
                  )}
                  {k.revoked_at ? (
                    <Button variant="secondary" size="sm" onClick={() => act(() => admin.unrevoke(k.key))}>
                      Unrevoke
                    </Button>
                  ) : (
                    <Button variant="danger" size="sm" onClick={() => act(() => admin.revoke(k.key))}>
                      Revoke
                    </Button>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}

function AdminsPanel({
  admins,
  owner,
  isOwner,
  act,
}: {
  admins: AdminRow[];
  owner: string;
  isOwner: boolean;
  act: (fn: () => Promise<unknown>) => Promise<boolean>;
}) {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const isOwnerRow = (a: AdminRow) => a.email.toLowerCase() === owner.toLowerCase();

  async function grant() {
    setBusy(true);
    if (await act(() => admin.setAdmin(email.trim(), true))) setEmail("");
    setBusy(false);
  }

  return (
    <Panel>
      <PanelHeader title="Admins" description={isOwner ? undefined : "Only the owner can change this list."} />
      <ul className="divide-y divide-border">
        {admins.map((a) => (
          <li key={a.email} className="flex min-h-[52px] items-center justify-between gap-3 px-4 py-2 sm:px-5">
            <div className="min-w-0">
              <p className="truncate text-sm text-fg">{a.name || a.email}</p>
              <p className="truncate text-[13px] text-fg-muted">
                {a.email}
                {isOwnerRow(a) && " · Owner"}
              </p>
            </div>
            {isOwner && !isOwnerRow(a) && (
              <Button variant="danger" size="sm" onClick={() => act(() => admin.setAdmin(a.email, false))}>
                Remove
              </Button>
            )}
          </li>
        ))}
      </ul>
      {isOwner && (
        <form
          className="flex flex-col gap-3 border-t border-border p-4 sm:flex-row sm:items-start sm:p-5"
          onSubmit={(e) => {
            e.preventDefault();
            grant();
          }}
        >
          <div className="min-w-0 flex-1">
            <TextField
              label="Add an admin"
              type="email"
              placeholder="their@gmail.com"
              hint="They need to sign in to the site once first."
              value={email}
              onChange={setEmail}
            />
          </div>
          <Button type="submit" variant="secondary" className="sm:mt-[26px]" loading={busy} disabled={!email.trim()}>
            {!busy && <UserPlus />}
            Add
          </Button>
        </form>
      )}
    </Panel>
  );
}
