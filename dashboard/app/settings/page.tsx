"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { parse as yamlParse, stringify as yamlStringify } from "yaml";
import { auth, farm } from "@/lib/api";
import {
  COMMAND_TOGGLES,
  GrinderConfig,
  RANDOM_EVENT_HANDLERS,
} from "@/lib/config";
import { cn } from "@/lib/utils";
import { Sidebar } from "@/components/sidebar";

// Settings — form-driven editor for the user's single farm config.
// Loads yaml from backend, mutates via typed form fields, serializes
// back to yaml on save. Fields the UI doesn't know about round-trip
// untouched (the whole object is kept between load → save).
export default function Settings() {
  const router = useRouter();
  const [me, setMe] = useState<{ email: string; picture: string } | null>(null);
  const [cfg, setCfg] = useState<GrinderConfig | null>(null);
  const [loading, setLoading] = useState(true);
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
      try {
        const yaml = await farm.getConfig();
        setCfg(yamlParse(yaml) ?? {});
      } catch (e) {
        setError("Config load failed: " + (e as Error).message);
      } finally {
        setLoading(false);
      }
    })();
  }, [router]);

  const update = (fn: (c: GrinderConfig) => void) => {
    if (!cfg) return;
    const next = structuredClone(cfg);
    fn(next);
    setCfg(next);
    setSavedAt(null);
  };

  const cluster = cfg?.clusters?.[0];

  async function save() {
    if (!cfg) return;
    setSaving(true);
    setError(null);
    try {
      const yaml = yamlStringify(cfg);
      await farm.saveConfig(yaml);
      setSavedAt(Date.now());
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex min-h-dvh">
      <Sidebar page="settings" me={me as { email: string; picture: string } | null} />
      <main className="flex-1 overflow-auto p-8">
        <header className="mb-6 flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-semibold">Settings</h1>
            <p className="text-sm text-muted">Configure your farm</p>
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

        {loading || !cfg ? (
          <p className="text-muted">Loading config...</p>
        ) : (
          <div className="mx-auto grid max-w-3xl gap-6">
            {/* Discord */}
            <Section title="Discord" subtitle="Where to grind + which account to grind as">
              <Field label="Guild (server) ID">
                <Input
                  value={cluster?.guild_id ?? ""}
                  onChange={(v) => update((c) => (ensureCluster(c).guild_id = v))}
                  placeholder="e.g. 1240218048305299517"
                />
                <Hint>Right-click your server icon → Copy Server ID (Developer Mode on).</Hint>
              </Field>
              <Field label="Channel ID">
                <Input
                  value={cluster?.channel_id ?? ""}
                  onChange={(v) => update((c) => (ensureCluster(c).channel_id = v))}
                  placeholder="e.g. 1240218096103329793"
                />
                <Hint>The channel where /flow or /fish runs.</Hint>
              </Field>
              <Field label="Discord token">
                <Input
                  type="password"
                  value={cluster?.master?.token ?? ""}
                  onChange={(v) => update((c) => (ensureCluster(c).master.token = v))}
                  placeholder="your self-bot token"
                />
                <Hint>Stored on the server. Never shared.</Hint>
              </Field>
            </Section>

            {/* Commands */}
            <Section title="Commands" subtitle="Which commands to run">
              <p className="mb-3 text-sm text-muted">
                <span className="text-foreground">flow</span> and <span className="text-foreground">fish</span> are
                exclusive — turning one on turns the other off and ignores beg/search/hunt.
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

            {/* Fishing */}
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
                  <Hint>Which sell button the grinder picks when a bucket fills.</Hint>
                </Field>
              </Section>
            )}

            {/* Breaks */}
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
                note="Simulates fatigue — scattered short breaks + big sleep-sized breaks on long runs."
              />
            </Section>

            {/* Random Events */}
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
                note="Master switch for Dank Random Event auto-responders."
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

            {/* Captcha */}
            <Section title="Captcha Alerts" subtitle="Get pinged when DM throws a captcha">
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
          </div>
        )}
      </main>
    </div>
  );
}

// --- primitives ---

function Section({ title, subtitle, children }: { title: string; subtitle?: string; children: React.ReactNode }) {
  return (
    <section className="rounded-xl border border-border bg-card p-6">
      <h2 className="text-sm font-medium uppercase text-muted">{title}</h2>
      {subtitle && <p className="mb-4 mt-1 text-xs text-muted">{subtitle}</p>}
      {!subtitle && <div className="mb-4" />}
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
