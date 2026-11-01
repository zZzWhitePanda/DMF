// Grinder config shape — mirrors backend/internal/grinder/config/config.go.
// We parse yaml into this shape on load, mutate via the settings form,
// and serialize back to yaml on save. Only the fields the Settings page
// actually surfaces are typed; unknown fields in the yaml are
// round-tripped untouched via the `_raw` escape hatch.

export interface GrinderConfig {
  clusters: Cluster[];
  humanize?: Humanize;
  captcha?: Captcha;
  features?: Features;
  log?: { level?: string; file?: string };
  // Preserve any top-level keys we don't know about so round-trips don't drop them.
  [key: string]: unknown;
}

export interface Cluster {
  name: string;
  guild_id: string;
  channel_id: string;
  master: { name: string; token: string; proxy?: string };
  secondary?: unknown[];
  shift?: Shift;
}

export interface Shift {
  enabled?: boolean;
  on_duration?: string;
  off_duration?: string;
  variation?: string;
}

export interface Humanize {
  typing_indicator?: boolean;
  min_delay?: string;
  max_delay?: string;
  typing_variance?: string;
}

export interface Captcha {
  webhook?: string;
  pause_on_detect?: boolean;
  mention_user_id?: string;
  desktop_notify?: boolean;
}

export interface Features {
  commands?: Record<string, boolean>;
  fishing?: FishingConfig;
  breaks?: { enabled?: boolean };
  random_events?: boolean;
  random_event_role_id?: string;
  random_event_handlers?: Record<string, boolean>;
  auto_buy?: Record<string, boolean>;
  proxies?: string[];
}

export interface FishingConfig {
  sell_mode?: "fish_tokens" | "coins";
  sell_strategy?: "bulk" | "per_fish";
  auto_buy_rod?: boolean;
  stop_on_break?: boolean;
}

export const RANDOM_EVENT_HANDLERS: { key: string; label: string }[] = [
  { key: "first_to_say", label: "First to Say" },
  { key: "reverse_reverse", label: "Reverse Reverse" },
  { key: "fortnite_dance", label: "Fortnite Dance Mode" },
  { key: "punch_pepe", label: "Punch Pepe" },
  { key: "steal_mels_beard", label: "Steal Mel's Beard" },
  { key: "scrambled_eggs", label: "Dank Scrambled Eggs" },
  { key: "trivia_night", label: "Trivia Night" },
];

export const COMMAND_TOGGLES: { key: string; label: string; exclusive?: boolean; note?: string }[] = [
  { key: "flow", label: "flow", exclusive: true, note: "exclusive — overrides other commands" },
  { key: "fish", label: "fish", exclusive: true, note: "exclusive — overrides other commands" },
  { key: "beg", label: "beg" },
  { key: "search", label: "search" },
  { key: "hunt", label: "hunt" },
];
