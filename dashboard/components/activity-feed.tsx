"use client";

import { memo, useMemo, useState } from "react";
import {
  Check,
  CircleX,
  Coffee,
  Coins,
  Dices,
  Fish,
  Hourglass,
  Info,
  LucideIcon,
  Moon,
  OctagonAlert,
  Package,
  Play,
  PlugZap,
  ShieldAlert,
  Sun,
  TriangleAlert,
  Unplug,
  Waypoints,
} from "lucide-react";
import type { GrinderEvent } from "@/lib/api";
import type { FeedEvent } from "@/lib/farm-session";
import { cn, fmtCommas, fmtTime } from "@/lib/utils";
import { Panel, PanelHeader, Segmented, TONE_TEXT, Tone } from "@/components/ui";

type Filter = "all" | "rewards" | "issues";

interface Line {
  icon: LucideIcon;
  tone: Tone;
  group: "rewards" | "issues" | "system";
  title: string;
  detail?: string;
  value?: string;
}

const RENDERED_ROWS = 200;

const itemList = (items: GrinderEvent["items"]) =>
  Object.entries(items ?? {})
    .map(([name, qty]) => `${name} ×${qty}`)
    .join(", ");

function describe(ev: GrinderEvent): Line {
  switch (ev.kind) {
    case "cmd_success":
      return { icon: Coins, tone: "live", group: "rewards", title: ev.command, value: `+${fmtCommas(ev.reward)}` };
    case "item":
      return { icon: Package, tone: "live", group: "rewards", title: "Items", detail: itemList(ev.items) };
    case "fish_token":
      return { icon: Coins, tone: "live", group: "rewards", title: "Fish tokens", value: `+${fmtCommas(ev.reward)}` };
    case "fish_caught":
      return { icon: Fish, tone: "live", group: "rewards", title: "Caught", detail: itemList(ev.items) };
    case "highlow_outcome":
      return { icon: Dices, tone: "neutral", group: "system", title: "High-low", detail: ev.outcome };
    case "highlow_decision":
      return { icon: Dices, tone: "neutral", group: "system", title: "High-low", detail: ev.message };
    case "cmd_cooldown":
      return { icon: Hourglass, tone: "neutral", group: "system", title: `${ev.command} on cooldown`, value: `${ev.reward}s` };
    case "cmd_fail":
      return { icon: CircleX, tone: "danger", group: "issues", title: `${ev.command} failed`, detail: ev.message };
    case "warn":
      return { icon: TriangleAlert, tone: "warn", group: "issues", title: ev.message };
    case "error":
      return { icon: OctagonAlert, tone: "danger", group: "issues", title: ev.message };
    case "captcha":
      return { icon: ShieldAlert, tone: "danger", group: "issues", title: "Captcha detected" };
    case "rod_broken":
      return { icon: TriangleAlert, tone: "warn", group: "issues", title: "Rod broke", detail: ev.message };
    case "flow_picker":
      return {
        icon: Waypoints,
        tone: "warn",
        group: "issues",
        title: "Flow is waiting for a choice",
        detail: ev.choices?.join(", "),
      };
    case "break_start":
      return { icon: Coffee, tone: "neutral", group: "system", title: ev.message };
    case "break_end":
      return { icon: Play, tone: "neutral", group: "system", title: ev.message };
    case "shift_on":
      return { icon: Sun, tone: "neutral", group: "system", title: "Shift started" };
    case "shift_off":
      return { icon: Moon, tone: "neutral", group: "system", title: "Shift ended, sleeping" };
    case "gateway_up":
      return { icon: PlugZap, tone: "neutral", group: "system", title: "Connected to Discord", detail: ev.message };
    case "gateway_down":
      return { icon: Unplug, tone: "warn", group: "issues", title: "Discord disconnected", detail: ev.message };
    case "success":
      return { icon: Check, tone: "neutral", group: "system", title: ev.message };
    default:
      return { icon: Info, tone: "neutral", group: "system", title: ev.message || ev.kind };
  }
}

const FeedRow = memo(function FeedRow({ ev, running }: { ev: FeedEvent; running: boolean }) {
  const { icon: Icon, tone, title, detail, value } = describe(ev);
  // A stopped farm is monochrome, so its past events go quiet with it.
  const iconTone = running ? tone : "neutral";
  return (
    <li className="row-enter flex items-start gap-3 px-4 py-1.5 transition-colors hover:bg-surface-raised sm:px-5">
      <time className="font-mono text-xs leading-5 tabular-nums text-fg-subtle">{fmtTime(ev.time)}</time>
      <Icon className={cn("mt-0.5 size-4 shrink-0", TONE_TEXT[iconTone])} strokeWidth={1.75} aria-hidden />
      <p className="min-w-0 flex-1 break-words text-[13px] leading-5 text-fg">
        {title}
        {detail && <span className="text-fg-muted"> {detail}</span>}
      </p>
      {value && <span className="font-mono text-[13px] leading-5 tabular-nums text-fg">{value}</span>}
    </li>
  );
});

export function ActivityFeed({ events, running }: { events: FeedEvent[]; running: boolean }) {
  const [filter, setFilter] = useState<Filter>("all");

  const issues = useMemo(() => events.filter((ev) => describe(ev).group === "issues").length, [events]);

  const rows = useMemo(() => {
    const matching = filter === "all" ? events : events.filter((ev) => describe(ev).group === filter);
    return matching.slice(-RENDERED_ROWS).reverse();
  }, [events, filter]);

  return (
    <Panel className="flex h-[440px] min-w-0 flex-col">
      <PanelHeader
        title="Activity"
        action={
          <Segmented
            label="Filter activity"
            value={filter}
            onChange={setFilter}
            options={[
              { value: "all", label: "All" },
              { value: "rewards", label: "Rewards" },
              {
                value: "issues",
                label: (
                  <>
                    Issues
                    {issues > 0 && (
                      <span className={cn("font-mono text-xs tabular-nums", TONE_TEXT[running ? "warn" : "neutral"])}>
                        {issues}
                      </span>
                    )}
                  </>
                ),
              },
            ]}
          />
        }
      />
      {rows.length === 0 ? (
        <p className="flex flex-1 items-center justify-center px-6 text-center text-[13px] text-fg-muted">
          {filter === "issues" && events.length > 0
            ? "No issues this session."
            : running
              ? "Waiting for the first event."
              : "Events stream here while the farm is running."}
        </p>
      ) : (
        <ul className="scrollbar-thin min-h-0 flex-1 overflow-y-auto py-1.5" aria-label="Recent events">
          {rows.map((ev) => (
            <FeedRow key={ev.id} ev={ev} running={running} />
          ))}
        </ul>
      )}
    </Panel>
  );
}
