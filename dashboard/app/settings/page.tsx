"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { parse as yamlParse, stringify as yamlStringify } from "yaml";
import {
  Account,
  accounts as accountsAPI,
  auth,
} from "@/lib/api";
import {
  COMMAND_TOGGLES,
  GrinderConfig,
  RANDOM_EVENT_HANDLERS,
} from "@/lib/config";
import { cn } from "@/lib/utils";

// Settings — form-driven editor for a Discord account's grinder config.
// Loads yaml from backend, mutates via typed form fields, serializes
// back to yaml on save. Fields the UI doesn't know about round-trip
// untouched (the whole object is kept between load → save).
export default function Settings() {
  const router = useRouter();
  const [me, setMe] = useState<{ email: string; picture: string } | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [cfg, setCfg] = useState<GrinderConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedAt, setSavedAt] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      const info = await auth.me();
      if (!info.signed_in) {
        router.replace("/");
        return;
      }
      setMe({ email: info.user?.email ?? "", picture: info.user?.picture ?? "" });
      const list = await accountsAPI.list();
      setAccounts(list);
      if (list.length > 0) setSelectedID(list[0].id);
    })();
  }, [router]);

  useEffect(() => {
    if (!selectedID) return;
    setLoading(true);
    setError(null);
    accountsAPI
      .getConfig(selectedID)
      .then((yaml) => {
        try {
          setCfg(yamlParse(yaml) ?? {});
        } catch (e) {
          setError("Config YAML on this account is malformed: " + (e as Error).message);
        }
      })
      .finally(() => setLoading(false));
  }, [selectedID]);

  // --- mutation helpers ---
  const update = (fn: (c: GrinderConfig) => void) => {
    if (!cfg) return;
    const next = structuredClone(cfg);
    fn(next);
    setCfg(next);
    setSavedAt(null);
  };

  const cluster = cfg?.clusters?.[0];

  async function save() {
    if (!cfg || !selectedID) return;
    setSaving(true);
    setError(null);
    try {
      const yaml = yamlStringify(cfg);
      await accountsAPI.saveConfig(selectedID, yaml);
      setSavedAt(Date.now());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex min-h-dvh">
      <aside className="w-64 border-r border-border bg-card px-4 py-6">
        <div className="mb-6 flex items-center gap-3">
          <div className="h-6 w-6 rounded bg-accent" />
          <span className="font-semibold">Dank Memer Farm</span>
        </div>
        <nav className="flex flex-col gap-1">
          <a href="/dashboard/" className="rounded-md px-3 py-2 text-sm text-muted hover:bg-border/40 hover:text-foreground">
            Dashboard
          </a>
          <span className="rounded-md px-3 py-2 text-sm bg-accent/15 text-foreground">Settings</span>
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
          </ul>
        </div>
      </aside>

      <main className="flex-1 overflow-auto p-8">
        <header className="mb-6 flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-semibold">Settings</h1>
            <p className="text-sm text-muted">
              {accounts.find((a) => a.id === selectedID)?.name ?? "No account selected"}
            </p>
          </div>
          <div className="flex items-center gap-3">
            {savedAt && !saving && <span className="text-sm text-success">Saved</span>}
            {error && <span className="text-sm text-error">{error}</span>}
            <button
              onClick={save}
              disabled={!cfg || saving}
              className={cn(
                "rounded-md px-4 py-2 text-sm font-medium",
                cfg && !saving
                  ? "bg-accent text-accent-foreground hover:brightness-110"
                  : "bg-border text-muted cursor-not-allowed",
              )}
            >
              {saving ? "Saving..." : "Save"}
            </button>
          </div>
        </header>

        {!selectedID ? (
          <p className="text-muted">Pick an account from the sidebar first. Or add one from the Dashboard page.</p>
        ) : loading || !cfg ? (
          <p className="text-muted">Loading config...</p>
        ) : (
          <div className="grid gap-6">
            {/* --- Discord --- */}
            <Section title="Discord">
              <Field label="Guild (server) ID">
                <Input
                  value={cluster?.guild_id ?? ""}
                  onChange={(v) => update((c) => ensureCluster(c).guild_id = v)}
                  placeholder="e.g. 1240218048305299517"
                />
                <Hint>Right-click your server icon in Discord → Copy Server ID (Developer Mode required).</Hint>
              </Field>
              <Field label="Channel ID">
                <Input
                  value={cluster?.channel_id ?? ""}
                  onChange={(v) => update((c) => ensureCluster(c).channel_id = v)}
                  placeholder="e.g. 1240218096103329793"
                />
                <Hint>Right-click the channel → Copy Channel ID. This is where /flow or /fish will run.</Hint>
              </Field>
              <Field label="Discord token">
                <Input
                  type="password"
                  value={cluster?.master?.token ?? ""}
                  onChange={(v) => update((c) => ensureCluster(c).master.token = v)}
                  placeholder="your self-bot token"
                />
                <Hint>Never share this. Stored encrypted-at-rest on the server volume.</Hint>
              </Field>
            </Section>

            {/* --- Commands --- */}
            <Section title="Commands">
              <p className="mb-3 text-sm text-muted">
                Enable the commands you want to grind. <span className="text-foreground">flow</span> and{" "}
                <span className="text-foreground">fish</span> are exclusive — turning one on turns the other off and ignores beg/search/hunt.
              </p>
              <div className="flex flex-col gap-2">
                {COMMAND_TOGGLES.map((cmd) => {
                  const enabled = !!cfg.features?.commands?.[cmd.key];
                  const exclusive = cfg.features?.commands?.flow || cfg.features?.commands?.fish;
                  const suppressed = exclusive && !cmd.exclusive;
                  return (
                    <Toggle
                      key={cmd.key}
                      label={cmd.label}
                      checked={enabled}
                      dimmed={suppressed}
                      note={cmd.note}
                      onChange={(v) =>
                        update((c) => {
                          c.features ??= {};
                          c.features.commands ??= {};
                          c.features.commands[cmd.key] = v;
                          if (v && cmd.key === "flow") c.features.commands.fish = false;
                          if (v && cmd.key === "fish") c.features.commands.flow = false;
                        })
                      }
                    />
                  );
                })}
              </div>
            </Section>

            {/* --- Fishing --- */}
            {cfg.features?.commands?.fish && (
              <Section title="Fishing">
                <Field label="Sell mode">
                  <Select
                    value={cfg.features?.fishing?.sell_mode ?? "fish_tokens"}
                    onChange={(v) =>
                      update((c) => {
                        c.features ??= {};
                        c.features.fishing ??= {};
                        c.features.fishing.sell_mode = v as "fish_tokens" | "coins";
                      })
                    }
                    options={[
                      { value: "fish_tokens", label: "Fish Tokens" },
                      { value: "coins", label: "Coins" },
                    ]}
                  />
                  <Hint>Which Sell button the grinder clicks when a bucket fills.</Hint>
                </Field>
              </Section>
            )}

            {/* --- Breaks --- */}
            <Section title="Humanized Breaks">
              <Toggle
                label="Enable humanized breaks"
                checked={!!cfg.features?.breaks?.enabled}
                onChange={(v) =>
                  update((c) => {
                    c.features ??= {};
                    c.features.breaks ??= {};
                    c.features.breaks.enabled = v;
                  })
                }
                note="Simulates fatigue — short breaks every so often, big sleep-sized breaks on long runs."
              />
            </Section>

            {/* --- Random Events --- */}
            <Section title="Random Events">
              <Toggle
                label="Random Events (master)"
                checked={!!cfg.features?.random_events}
                onChange={(v) =>
                  update((c) => {
                    c.features ??= {};
                    c.features.random_events = v;
                  })
                }
                note="Master switch for auto-responding to Dank Random Events."
              />
              <Field label="Event Role ID">
                <Input
                  value={cfg.features?.random_event_role_id ?? ""}
                  onChange={(v) =>
                    update((c) => {
                      c.features ??= {};
                      c.features.random_event_role_id = v;
                    })
                  }
                  placeholder="role id DM pings on new events"
                />
                <Hint>If set, we only respond when this role is actually pinged. Prevents false positives.</Hint>
              </Field>
              <div className="mt-3">
                <div className="mb-2 text-xs font-medium uppercase text-muted">Per-event toggles</div>
                <div className="flex flex-col gap-2">
                  {RANDOM_EVENT_HANDLERS.map((h) => {
                    const masterOff = !cfg.features?.random_events;
                    const enabled = cfg.features?.random_event_handlers?.[h.key] !== false;
                    return (
                      <Toggle
                        key={h.key}
                        label={"↳ " + h.label}
                        checked={enabled}
                        dimmed={masterOff}
                        onChange={(v) =>
                          update((c) => {
                            c.features ??= {};
                            c.features.random_event_handlers ??= {};
                            c.features.random_event_handlers[h.key] = v;
                          })
                        }
                      />
                    );
                  })}
                </div>
              </div>
            </Section>

            {/* --- Captcha --- */}
            <Section title="Captcha Alerts">
              <Field label="Discord webhook URL">
                <Input
                  value={cfg.captcha?.webhook ?? ""}
                  onChange={(v) =>
                    update((c) => {
                      c.captcha ??= {};
                      c.captcha.webhook = v;
                    })
                  }
                  placeholder="https://discord.com/api/webhooks/..."
                />
                <Hint>Posts a message when a captcha hits so you can solve it.</Hint>
              </Field>
              <Field label="Your Discord user ID (to @ mention)">
                <Input
                  value={cfg.captcha?.mention_user_id ?? ""}
                  onChange={(v) =>
                    update((c) => {
                      c.captcha ??= {};
                      c.captcha.mention_user_id = v;
                    })
                  }
                  placeholder="e.g. 843039944955527189"
                />
              </Field>
            </Section>

            {/* --- Account danger zone --- */}
            <Section title="Account">
              <p className="mb-3 text-sm text-muted">
                Delete this account from the farm. The grinder stops first, then the config is removed.
              </p>
              <button
                onClick={async () => {
                  if (!selectedID) return;
                  if (!confirm("Delete this account? This can't be undone.")) return;
                  await accountsAPI.del(selectedID);
                  const list = await accountsAPI.list();
                  setAccounts(list);
                  setSelectedID(list[0]?.id ?? null);
                }}
                className="rounded-md border border-error/50 bg-error/10 px-4 py-2 text-sm text-error hover:bg-error/20"
              >
                Delete account
              </button>
            </Section>
          </div>
        )}
      </main>
    </div>
  );
}

// --- primitives ---

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="mb-4 text-sm font-medium uppercase text-muted">{title}</h2>
      <div className="flex flex-col gap-4">{children}</div>
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-sm font-medium text-foreground">{label}</span>
      {children}
    </label>
  );
}

function Hint({ children }: { children: React.ReactNode }) {
  return <span className="text-xs text-muted">{children}</span>;
}

function Input({
  value,
  onChange,
  placeholder,
  type = "text",
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
}) {
  return (
    <input
      type={type}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className="rounded-md border border-border bg-background px-3 py-2 text-sm font-mono outline-none focus:border-accent"
    />
  );
}

function Select({
  value,
  onChange,
  options,
}: {
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:border-accent"
    >
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

function Toggle({
  label,
  checked,
  onChange,
  note,
  dimmed,
}: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  note?: string;
  dimmed?: boolean;
}) {
  return (
    <label className={cn("flex cursor-pointer items-start gap-3", dimmed && "opacity-50")}>
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5 h-4 w-4 rounded border-border bg-background accent-accent"
      />
      <span className="flex flex-col">
        <span className="text-sm text-foreground">{label}</span>
        {note && <span className="text-xs text-muted">{note}</span>}
      </span>
    </label>
  );
}

function ensureCluster(c: GrinderConfig): import("@/lib/config").Cluster {
  c.clusters ??= [];
  if (c.clusters.length === 0) {
    c.clusters.push({
      name: "main",
      guild_id: "",
      channel_id: "",
      master: { name: "alpha", token: "" },
    });
  }
  const cl = c.clusters[0];
  cl.master ??= { name: "alpha", token: "" };
  return cl;
}
