"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { parse as yamlParse, stringify as yamlStringify } from "yaml";
import { Check, ChevronDown, Fish, Layers, Plus, Trash2, TriangleAlert, Waypoints } from "lucide-react";
import { farm } from "@/lib/api";
import { AutoUseItem, COMMAND_TOGGLES, Cluster, GrinderConfig, RANDOM_EVENT_HANDLERS } from "@/lib/config";
import { useUser } from "@/lib/use-user";
import { cn } from "@/lib/utils";
import { AppShell } from "@/components/app-shell";
import {
  Button,
  INPUT_STYLES,
  Panel,
  PanelHeader,
  Segmented,
  ServerError,
  Skeleton,
  TextField,
  ToggleRow,
} from "@/components/ui";

const SECTIONS = [
  { id: "discord", label: "Discord" },
  { id: "commands", label: "Commands" },
  { id: "breaks", label: "Breaks" },
  { id: "items", label: "Items" },
  { id: "events", label: "Random events" },
  { id: "alerts", label: "Captcha alerts" },
];

type Mode = "classic" | "flow" | "fish";

// Mirrors commands.Build in the backend: flow wins, then fish, and the
// individual commands only run when neither is on.
const MODES: { value: Mode; label: string; note: string; icon: typeof Fish }[] = [
  { value: "classic", label: "Classic", note: "Cycles through the commands you pick.", icon: Layers },
  { value: "flow", label: "Flow", note: "One long /flow session. Nothing else runs.", icon: Waypoints },
  { value: "fish", label: "Fish", note: "One long /fish session. Nothing else runs.", icon: Fish },
];

const SNOWFLAKE = /^\d{17,20}$/;
const WEBHOOK = /^https:\/\/([a-z]+\.)?discord(app)?\.com\/api\/webhooks\//;

const checkId = (v: string) => (SNOWFLAKE.test(v.trim()) ? null : "Discord IDs are 17 to 20 digits.");
const checkWebhook = (v: string) =>
  WEBHOOK.test(v.trim()) ? null : "Expected a discord.com/api/webhooks/ URL.";

// Settings — form-driven editor for the user's single farm config.
// Loads yaml from backend, mutates via typed form fields, serializes
// back to yaml on save. Fields the UI doesn't know about round-trip
// untouched (the whole object is kept between load → save).
export default function Settings() {
  const { user, failed, retry } = useUser();
  const [cfg, setCfg] = useState<GrinderConfig | null>(null);
  const [saved, setSaved] = useState<GrinderConfig | null>(null);
  const [running, setRunning] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Set by a save blocked on bad items, so rows nobody touched show why.
  const [showItemErrors, setShowItemErrors] = useState(false);

  const load = useCallback(async () => {
    setLoadError(null);
    try {
      const [yaml, summary] = await Promise.all([farm.getConfig(), farm.summary()]);
      const parsed: GrinderConfig = yamlParse(yaml) ?? {};
      setCfg(parsed);
      setSaved(parsed);
      setRunning(summary.is_running);
    } catch (e) {
      setLoadError((e as Error).message);
    }
  }, []);

  useEffect(() => {
    if (user) load();
  }, [user, load]);

  const dirty = useMemo(() => JSON.stringify(cfg) !== JSON.stringify(saved), [cfg, saved]);

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  useEffect(() => {
    if (!justSaved) return;
    const t = setTimeout(() => setJustSaved(false), 6000);
    return () => clearTimeout(t);
  }, [justSaved]);

  const update = (fn: (c: GrinderConfig) => void) => {
    if (!cfg) return;
    const next = structuredClone(cfg);
    fn(next);
    setCfg(next);
    setJustSaved(false);
    setError(null);
  };

  const updateFeatures = (fn: (f: NonNullable<GrinderConfig["features"]>) => void) =>
    update((c) => fn((c.features ??= {})));

  async function save() {
    if (!cfg) return;
    const items = cfg.features?.auto_use?.items ?? [];
    if (items.some((_, i) => Object.values(itemErrors(items, i)).some(Boolean))) {
      setShowItemErrors(true);
      setError("Fix the items marked in red.");
      document.getElementById("items")?.scrollIntoView();
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await farm.saveConfig(yamlStringify(cfg));
      setSaved(cfg);
      setJustSaved(true);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  const ready = !!cfg;
  const activeSection = useActiveSection(ready);

  return (
    <AppShell page="settings" user={user}>
      {failed ? (
        <ServerError onRetry={retry} />
      ) : loadError ? (
        <ServerError onRetry={load} message={loadError} />
      ) : (
        <div>
          <div className="mb-6 sm:mb-8">
            <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
            <p className="mt-1 text-sm text-fg-muted">Changes apply the next time the farm starts.</p>
          </div>

          <div className="lg:grid lg:grid-cols-[180px_minmax(0,720px)] lg:gap-12">
            <nav aria-label="Settings sections" className="hidden lg:block">
              <ul className="sticky top-[88px] grid gap-0.5">
                {SECTIONS.map((s) => (
                  <li key={s.id}>
                    <a
                      href={`#${s.id}`}
                      aria-current={s.id === activeSection ? "true" : undefined}
                      className={cn(
                        "block rounded-md px-3 py-1.5 text-sm transition-colors",
                        s.id === activeSection
                          ? "bg-surface-raised font-medium text-fg"
                          : "text-fg-muted hover:text-fg",
                      )}
                    >
                      {s.label}
                    </a>
                  </li>
                ))}
              </ul>
            </nav>

            {!cfg ? (
              <SettingsSkeleton />
            ) : (
              <div className="min-w-0">
                <div className="grid gap-4 sm:gap-5">
                  <SettingsSection id="discord" title="Discord" description="Which account grinds, and where.">
                    <div className="grid gap-4 p-4 sm:p-5">
                      <TextField
                        label="Discord token"
                        secret
                        value={cfg.clusters?.[0]?.master?.token ?? ""}
                        // Stray spaces or quotes from pasting make Discord reject the token.
                        onChange={(v) => update((c) => (ensureCluster(c).master.token = v.trim().replace(/^"|"$/g, "")))}
                        placeholder="Paste your token"
                        hint="Stored on the server. Never shared."
                      />
                      <div className="grid gap-4 sm:grid-cols-2">
                        <TextField
                          label="Server ID"
                          mono
                          inputMode="numeric"
                          value={cfg.clusters?.[0]?.guild_id ?? ""}
                          onChange={(v) => update((c) => (ensureCluster(c).guild_id = v))}
                          validate={checkId}
                          placeholder="123456789012345678"
                          hint="Right-click the server, then Copy Server ID."
                        />
                        <TextField
                          label="Channel ID"
                          mono
                          inputMode="numeric"
                          value={cfg.clusters?.[0]?.channel_id ?? ""}
                          onChange={(v) => update((c) => (ensureCluster(c).channel_id = v))}
                          validate={checkId}
                          placeholder="123456789012345678"
                          hint="Where the farm runs its commands."
                        />
                      </div>
                    </div>
                  </SettingsSection>

                  <SettingsSection id="commands" title="Commands" description="How the farm spends its time.">
                    <CommandSettings cfg={cfg} updateFeatures={updateFeatures} />
                  </SettingsSection>

                  <SettingsSection id="breaks" title="Breaks">
                    <div className="px-4 py-2 sm:px-5">
                      <ToggleRow
                        label="Humanized breaks"
                        description="Scattered short pauses, plus long sleep-sized ones on long runs."
                        checked={!!cfg.features?.breaks?.enabled}
                        onChange={(v) => updateFeatures((f) => ((f.breaks ??= {}).enabled = v))}
                      />
                    </div>
                  </SettingsSection>

                  <SettingsSection
                    id="items"
                    title="Items"
                    description="Keep items like a Lucky Horseshoe going with /use."
                  >
                    <div className="border-b border-border px-4 py-2 sm:px-5">
                      <ToggleRow
                        label="Use items"
                        description="Uses each one before the farm starts, then again on its timer."
                        checked={!!cfg.features?.auto_use?.enabled}
                        onChange={(v) => updateFeatures((f) => ((f.auto_use ??= {}).enabled = v))}
                      />
                    </div>
                    <AutoUseList
                      items={cfg.features?.auto_use?.items ?? []}
                      showErrors={showItemErrors}
                      onChange={(fn) => updateFeatures((f) => fn(((f.auto_use ??= {}).items ??= [])))}
                    />
                  </SettingsSection>

                  <SettingsSection id="events" title="Random events">
                    <div className="border-b border-border px-4 py-2 sm:px-5">
                      <ToggleRow
                        label="Respond to random events"
                        description="Answers Dank Memer's random events automatically."
                        checked={!!cfg.features?.random_events}
                        onChange={(v) => updateFeatures((f) => (f.random_events = v))}
                      />
                    </div>
                    <div className="grid gap-5 p-4 sm:p-5">
                      <TextField
                        label="Event role ID"
                        mono
                        inputMode="numeric"
                        value={cfg.features?.random_event_role_id ?? ""}
                        onChange={(v) => updateFeatures((f) => (f.random_event_role_id = v))}
                        validate={checkId}
                        placeholder="Optional"
                        hint="When set, the farm only responds if this role is pinged. Prevents false positives."
                      />
                      <fieldset>
                        <legend className="mb-1 text-sm font-medium text-fg">Events to answer</legend>
                        <div className="grid gap-x-8 sm:grid-cols-2">
                          {RANDOM_EVENT_HANDLERS.map((h) => (
                            <ToggleRow
                              key={h.key}
                              label={<span className="font-normal">{h.label}</span>}
                              checked={cfg.features?.random_event_handlers?.[h.key] !== false}
                              disabled={!cfg.features?.random_events}
                              onChange={(v) => updateFeatures((f) => ((f.random_event_handlers ??= {})[h.key] = v))}
                            />
                          ))}
                        </div>
                      </fieldset>
                    </div>
                  </SettingsSection>

                  <SettingsSection id="alerts" title="Captcha alerts" description="Get pinged in Discord when a captcha pauses the farm.">
                    <div className="grid gap-4 p-4 sm:grid-cols-2 sm:p-5">
                      <div className="sm:col-span-2">
                        <TextField
                          label="Webhook URL"
                          type="url"
                          value={cfg.captcha?.webhook ?? ""}
                          onChange={(v) => update((c) => ((c.captcha ??= {}).webhook = v))}
                          validate={checkWebhook}
                          placeholder="https://discord.com/api/webhooks/..."
                          hint="Channel settings, then Integrations, then Webhooks."
                        />
                      </div>
                      <TextField
                        label="Your Discord user ID"
                        mono
                        inputMode="numeric"
                        value={cfg.captcha?.mention_user_id ?? ""}
                        onChange={(v) => update((c) => ((c.captcha ??= {}).mention_user_id = v))}
                        validate={checkId}
                        placeholder="123456789012345678"
                        hint="So the alert @mentions you."
                      />
                    </div>
                  </SettingsSection>
                </div>

                <SaveBar
                  dirty={dirty}
                  saving={saving}
                  justSaved={justSaved}
                  running={running}
                  error={error}
                  onSave={save}
                  onDiscard={() => {
                    setCfg(saved);
                    setError(null);
                    setShowItemErrors(false);
                  }}
                />
              </div>
            )}
          </div>
        </div>
      )}
    </AppShell>
  );
}

function CommandSettings({
  cfg,
  updateFeatures,
}: {
  cfg: GrinderConfig;
  updateFeatures: (fn: (f: NonNullable<GrinderConfig["features"]>) => void) => void;
}) {
  const commands = cfg.features?.commands;
  const mode: Mode = commands?.flow ? "flow" : commands?.fish ? "fish" : "classic";
  const classic = COMMAND_TOGGLES.filter((c) => !c.exclusive);
  const nothingToRun = mode === "classic" && !classic.some((c) => commands?.[c.key]);

  return (
    <div className="grid gap-4 p-4 sm:p-5">
      <fieldset>
        <legend className="sr-only">Grinding mode</legend>
        <div className="grid gap-2 sm:grid-cols-3">
          {MODES.map(({ value, label, note, icon: Icon }) => (
            <label
              key={value}
              className={cn(
                "relative flex cursor-pointer flex-col gap-1 rounded-md border p-3 transition-colors",
                "has-[:focus-visible]:outline has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-accent",
                mode === value
                  ? "border-fg-subtle bg-surface-raised"
                  : "border-border hover:border-border-strong",
              )}
            >
              <input
                type="radio"
                name="mode"
                value={value}
                checked={mode === value}
                onChange={() =>
                  updateFeatures((f) => {
                    const c = (f.commands ??= {});
                    c.flow = value === "flow";
                    c.fish = value === "fish";
                  })
                }
                className="sr-only"
              />
              <span className="flex items-center justify-between">
                <span className="flex items-center gap-2 text-sm font-medium text-fg">
                  <Icon className={cn("size-4", mode === value ? "text-fg" : "text-fg-subtle")} strokeWidth={1.75} />
                  {label}
                </span>
                {mode === value && <Check className="size-4 text-fg" aria-hidden />}
              </span>
              <span className="text-[13px] leading-snug text-fg-muted">{note}</span>
            </label>
          ))}
        </div>
      </fieldset>

      {mode === "classic" && (
        <fieldset className="border-t border-border pt-2">
          <legend className="sr-only">Commands to cycle through</legend>
          <div className="grid gap-x-8 sm:grid-cols-3">
            {classic.map((cmd) => (
              <ToggleRow
                key={cmd.key}
                label={<span className="font-mono text-[13px] font-normal">/{cmd.label}</span>}
                checked={!!commands?.[cmd.key]}
                onChange={(v) => updateFeatures((f) => ((f.commands ??= {})[cmd.key] = v))}
              />
            ))}
          </div>
          {nothingToRun && (
            <p className="mt-2 flex items-center gap-2 text-[13px] text-warn">
              <TriangleAlert className="size-4 shrink-0" />
              Turn on at least one command, or the farm has nothing to run.
            </p>
          )}
        </fieldset>
      )}

      {mode === "fish" && (
        <div className="flex flex-col gap-2 border-t border-border pt-4 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
          <div>
            <p className="text-sm font-medium text-fg">Sell for</p>
            <p className="mt-0.5 text-[13px] text-fg-muted">Which button to press when the bucket fills.</p>
          </div>
          <Segmented
            label="Sell for"
            value={cfg.features?.fishing?.sell_mode ?? "fish_tokens"}
            onChange={(v) => updateFeatures((f) => ((f.fishing ??= {}).sell_mode = v))}
            options={[
              { value: "fish_tokens", label: "Fish tokens" },
              { value: "coins", label: "Coins" },
            ]}
            className="w-full sm:w-auto"
          />
        </div>
      )}
    </div>
  );
}

// Mirrors config.MinAutoUseEvery on the server.
const MIN_EVERY_SECONDS = 10;
// On phones Every gets the bigger share, since it holds a number and a unit.
const ROW_GRID = "grid grid-cols-[minmax(0,3fr)_minmax(0,2fr)_2.75rem] sm:grid-cols-[minmax(0,1fr)_11rem_8rem_2.25rem]";

type EveryUnit = "s" | "m" | "h" | "d";
const UNITS: { value: EveryUnit; label: string; seconds: number }[] = [
  { value: "s", label: "sec", seconds: 1 },
  { value: "m", label: "min", seconds: 60 },
  { value: "h", label: "hr", seconds: 3600 },
  { value: "d", label: "days", seconds: 86400 },
];

/** Seconds in a duration as the server reads it ("15m", "1h30m0s", "2d"). NaN if it isn't one. */
function durationSeconds(d: string): number {
  const parts = [...d.matchAll(/(\d+(?:\.\d+)?)(d|h|ms|m|s)/g)];
  if (!d || parts.map((p) => p[0]).join("") !== d) return NaN;
  const secs: Record<string, number> = { d: 86400, h: 3600, m: 60, s: 1, ms: 0.001 };
  return parts.reduce((sum, [, n, u]) => sum + Number(n) * secs[u], 0);
}

/** An item's gap as a number and unit to show. The number is blank while unset. */
function splitEvery(d: string | undefined): { n: string; unit: EveryUnit } {
  const plain = d?.match(/^(\d+)([smhd])$/);
  if (plain) return { n: plain[1] === "0" ? "" : plain[1], unit: plain[2] as EveryUnit };
  const secs = durationSeconds(d ?? "");
  if (!(secs > 0)) return { n: "", unit: "m" };
  // Written some other way, like "1h30m0s": the biggest unit that fits evenly.
  const unit = [...UNITS].reverse().find((u) => secs % u.seconds === 0) ?? UNITS[0];
  return { n: String(Math.round((secs / unit.seconds) * 100) / 100), unit: unit.value };
}

/** Whole numbers only, without leading zeros. */
const digits = (v: string) => v.replace(/\D/g, "").replace(/^0+/, "");

function itemErrors(items: AutoUseItem[], i: number) {
  const name = items[i].name.trim().toLowerCase();
  const every = durationSeconds(items[i].every ?? "");
  return {
    name: !name
      ? "Enter the item's name."
      : items.some((o, j) => j < i && o.name.trim().toLowerCase() === name)
        ? "Already in the list."
        : null,
    every: !(every > 0)
      ? "Enter how often."
      : every < MIN_EVERY_SECONDS
        ? `At least ${MIN_EVERY_SECONDS} seconds.`
        : null,
  };
}

function AutoUseList({
  items,
  showErrors,
  onChange,
}: {
  items: AutoUseItem[];
  showErrors: boolean;
  onChange: (fn: (items: AutoUseItem[]) => void) => void;
}) {
  // Keys that survive deleting a row above, so no row inherits its
  // neighbour's errors. Rows from a load or Discard fall back to their index.
  const nextKey = useRef(0);
  const [keys, setKeys] = useState<number[]>([]);
  const rowKeys = items.map((_, i) => keys[i] ?? -(i + 1));

  const add = () => {
    setKeys([...rowKeys, nextKey.current++]);
    onChange((list) => list.push({ name: "", every: "15m" }));
  };
  const remove = (i: number) => {
    setKeys(rowKeys.filter((_, j) => j !== i));
    onChange((list) => list.splice(i, 1));
  };

  return (
    <div className="grid gap-3 p-4 sm:p-5">
      {items.length > 0 && (
        <div className="grid gap-2">
          <div aria-hidden className={cn(ROW_GRID, "hidden gap-x-2 text-[13px] text-fg-muted sm:grid")}>
            <span>Item</span>
            <span>Every</span>
            <span>Stop after</span>
          </div>
          <ul className="grid gap-4 sm:gap-2">
            {items.map((item, i) => (
              <AutoUseRow
                key={rowKeys[i]}
                index={i}
                item={item}
                errors={itemErrors(items, i)}
                showErrors={showErrors}
                onChange={(fn) => onChange((list) => fn(list[i]))}
                onRemove={() => remove(i)}
              />
            ))}
          </ul>
        </div>
      )}
      <div>
        <Button variant="secondary" size="sm" onClick={add}>
          <Plus />
          Add item
        </Button>
      </div>
      <p className="text-[13px] text-fg-muted">
        Type the name as you would after <span className="font-mono">/use item:</span>. When an item reaches its limit
        or runs out, the farm stops using it until you press Continue on the Farm page, and keeps grinding meanwhile.
      </p>
    </div>
  );
}

function AutoUseRow({
  index,
  item,
  errors,
  showErrors,
  onChange,
  onRemove,
}: {
  index: number;
  item: AutoUseItem;
  errors: { name: string | null; every: string | null };
  showErrors: boolean;
  onChange: (fn: (item: AutoUseItem) => void) => void;
  onRemove: () => void;
}) {
  const id = `auto-use-${index}`;
  const [touched, setTouched] = useState({ name: false, every: false });
  const every = splitEvery(item.every);
  const nameError = touched.name || showErrors ? errors.name : null;
  const everyError = touched.every || showErrors ? errors.every : null;
  // Phone labels sit above each input; wider screens share one header row.
  const labelClass = "mb-1.5 block text-[13px] leading-5 text-fg-muted sm:sr-only";
  const suffixClass = "pointer-events-none absolute inset-y-0 right-3 flex items-center text-[13px] text-fg-subtle";

  return (
    <li className={cn(ROW_GRID, "gap-x-2 gap-y-3 border-b border-border pb-4 last:border-0 last:pb-0 sm:border-0 sm:pb-0")}>
      <div className="col-span-2 min-w-0 sm:col-span-1">
        <label htmlFor={`${id}-name`} className={labelClass}>
          Item {index + 1}
        </label>
        <input
          id={`${id}-name`}
          value={item.name}
          placeholder="Item name"
          autoComplete="off"
          spellCheck={false}
          aria-invalid={nameError ? true : undefined}
          aria-describedby={nameError ? `${id}-name-error` : undefined}
          onChange={(e) => onChange((it) => (it.name = e.target.value))}
          onBlur={() => setTouched((t) => ({ ...t, name: true }))}
          className={INPUT_STYLES}
        />
        {nameError && (
          <p id={`${id}-name-error`} className="mt-1.5 text-[13px] text-danger">
            {nameError}
          </p>
        )}
      </div>
      <Button
        variant="ghost"
        size="sm"
        aria-label={`Remove ${item.name.trim() || `item ${index + 1}`}`}
        title="Remove"
        onClick={onRemove}
        className="col-start-3 row-start-1 mt-[26px] h-11 w-11 px-0 sm:col-start-4 sm:mt-0 sm:h-9 sm:w-9"
      >
        <Trash2 />
      </Button>
      <div className="min-w-0">
        <label htmlFor={`${id}-every`} className={labelClass}>
          Every
        </label>
        {/* The unit stays in the saved value even while the number is blank ("0m"). */}
        <div className="flex">
          <input
            id={`${id}-every`}
            inputMode="numeric"
            value={every.n}
            autoComplete="off"
            aria-invalid={everyError ? true : undefined}
            aria-describedby={everyError ? `${id}-every-error` : undefined}
            onChange={(e) => {
              const n = digits(e.target.value);
              onChange((it) => (it.every = `${n || 0}${every.unit}`));
            }}
            onBlur={() => setTouched((t) => ({ ...t, every: true }))}
            className={cn(INPUT_STYLES, "relative min-w-0 flex-1 rounded-r-none tabular-nums focus:z-10")}
          />
          <div className="relative -ml-px shrink-0">
            <select
              aria-label={`Unit for ${item.name.trim() || `item ${index + 1}`}`}
              value={every.unit}
              onChange={(e) => onChange((it) => (it.every = `${every.n || 0}${e.target.value}`))}
              className={cn(
                INPUT_STYLES,
                "relative w-auto cursor-pointer appearance-none rounded-l-none pl-2 pr-6 text-fg-muted focus:z-10",
              )}
            >
              {UNITS.map((u) => (
                <option key={u.value} value={u.value}>
                  {u.label}
                </option>
              ))}
            </select>
            <ChevronDown
              aria-hidden
              className="pointer-events-none absolute right-1.5 top-1/2 z-10 size-3.5 -translate-y-1/2 text-fg-subtle"
            />
          </div>
        </div>
        {everyError && (
          <p id={`${id}-every-error`} className="mt-1.5 text-[13px] text-danger">
            {everyError}
          </p>
        )}
      </div>
      <div className="min-w-0">
        <label htmlFor={`${id}-limit`} className={labelClass}>
          Stop after
        </label>
        <div className="relative">
          <input
            id={`${id}-limit`}
            inputMode="numeric"
            value={item.limit ? String(item.limit) : ""}
            placeholder="No limit"
            autoComplete="off"
            onChange={(e) => {
              const n = Number(digits(e.target.value));
              onChange((it) => {
                if (n > 0) it.limit = n;
                else delete it.limit;
              });
            }}
            className={cn(INPUT_STYLES, "tabular-nums", !!item.limit && "pr-14")}
          />
          {!!item.limit && (
            <span aria-hidden className={suffixClass}>
              {item.limit === 1 ? "use" : "uses"}
            </span>
          )}
        </div>
      </div>
    </li>
  );
}

function SettingsSection({
  id,
  title,
  description,
  children,
}: {
  id: string;
  title: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <Panel id={id} aria-labelledby={`${id}-title`} className="scroll-mt-[72px]">
      <PanelHeader title={<span id={`${id}-title`}>{title}</span>} description={description} />
      {children}
    </Panel>
  );
}

function SaveBar({
  dirty,
  saving,
  justSaved,
  running,
  error,
  onSave,
  onDiscard,
}: {
  dirty: boolean;
  saving: boolean;
  justSaved: boolean;
  running: boolean;
  error: string | null;
  onSave: () => void;
  onDiscard: () => void;
}) {
  // Sticky inside the form column, so its edges always match the panels.
  return (
    <div aria-live="polite" className="sticky bottom-[calc(env(safe-area-inset-bottom)+16px)] z-20 mt-4 empty:hidden">
      {(dirty || justSaved) && (
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 rounded-lg border border-border-strong bg-surface-raised px-4 py-2.5 shadow-popover animate-in fade-in-0 slide-in-from-bottom-2">
          {dirty ? (
            <>
              <p className={cn("min-w-0 text-sm", error ? "text-danger" : "text-fg-muted")}>{error ?? "Unsaved changes"}</p>
              <div className="flex items-center gap-2">
                <Button variant="ghost" size="sm" onClick={onDiscard} disabled={saving}>
                  Discard
                </Button>
                <Button size="sm" onClick={onSave} loading={saving}>
                  Save changes
                </Button>
              </div>
            </>
          ) : (
            <p className="flex min-h-9 items-center gap-2 text-sm text-fg sm:min-h-8">
              <Check className="size-4 text-accent" aria-hidden />
              Saved.
              {running && (
                <a href="/dashboard/" className="text-fg-muted underline underline-offset-2 hover:text-fg">
                  Restart the farm to apply.
                </a>
              )}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function SettingsSkeleton() {
  return (
    <div className="grid gap-4 sm:gap-5" aria-busy="true" aria-label="Loading settings">
      <Skeleton className="h-64 rounded-lg" />
      <Skeleton className="h-52 rounded-lg" />
      <Skeleton className="h-24 rounded-lg" />
    </div>
  );
}

function useActiveSection(ready: boolean) {
  const [active, setActive] = useState(SECTIONS[0].id);
  useEffect(() => {
    if (!ready) return;
    const observer = new IntersectionObserver(
      (entries) => {
        const hit = entries.find((e) => e.isIntersecting);
        if (hit) setActive(hit.target.id);
      },
      { rootMargin: "-72px 0px -65% 0px" },
    );
    for (const s of SECTIONS) {
      const el = document.getElementById(s.id);
      if (el) observer.observe(el);
    }
    return () => observer.disconnect();
  }, [ready]);
  return active;
}

function ensureCluster(c: GrinderConfig): Cluster {
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
