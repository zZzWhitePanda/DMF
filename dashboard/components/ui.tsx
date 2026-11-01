"use client";

import * as React from "react";
import { Eye, EyeOff, RotateCw, ServerCrash } from "lucide-react";
import { cn } from "@/lib/utils";

// ---------------------------------------------------------------------------
// Logo — a staggered coin stack. Same geometry as app/icon.svg.
// ---------------------------------------------------------------------------

export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={cn("size-7", className)} aria-hidden>
      <rect width="32" height="32" rx="8" className="fill-accent" />
      <g className="fill-accent-ink">
        <rect x="7" y="20" width="15" height="5" rx="2.5" />
        <rect x="10" y="13.5" width="15" height="5" rx="2.5" />
        <rect x="7.5" y="7" width="15" height="5" rx="2.5" />
      </g>
    </svg>
  );
}

// ---------------------------------------------------------------------------
// Button — exported as a style helper too, so links can wear the same look.
// ---------------------------------------------------------------------------

/** Grows a compact control's tap area to 44px on touch screens without changing how it looks. */
const TOUCH_TARGET = "relative after:absolute after:-inset-y-1 after:inset-x-0 sm:after:hidden";

type ButtonStyle = {
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md" | "lg";
  className?: string;
};

export function buttonStyles({ variant = "primary", size = "md", className }: ButtonStyle = {}) {
  const sizes = {
    sm: cn("h-9 px-3 text-[13px] sm:h-8", TOUCH_TARGET),
    md: "h-11 px-4 text-sm sm:h-9",
    lg: "h-12 px-5 text-[15px]",
  };
  const variants = {
    primary: "bg-accent text-accent-ink hover:bg-accent-hover",
    secondary: "border border-border bg-surface-raised text-fg hover:border-border-strong hover:bg-surface-hover",
    ghost: "text-fg-muted hover:bg-surface-raised hover:text-fg",
    danger: "border border-danger/40 bg-danger/10 text-danger hover:bg-danger/20",
  };
  return cn(
    "inline-flex cursor-pointer select-none items-center justify-center gap-2 whitespace-nowrap rounded-md font-medium",
    "transition-colors active:opacity-80 disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0",
    sizes[size],
    variants[variant],
    className,
  );
}

type ButtonProps = React.ButtonHTMLAttributes<HTMLButtonElement> & ButtonStyle & { loading?: boolean };

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, loading, disabled, type = "button", children, ...rest }, ref) => (
    <button
      ref={ref}
      type={type}
      disabled={disabled || loading}
      className={buttonStyles({ variant, size, className })}
      {...rest}
    >
      {loading ? <Spinner /> : null}
      {children}
    </button>
  ),
);
Button.displayName = "Button";

// ---------------------------------------------------------------------------
// Panel — the one surface everything sits on.
// ---------------------------------------------------------------------------

export function Panel({ className, ...rest }: React.HTMLAttributes<HTMLElement>) {
  return <section className={cn("rounded-lg border border-border bg-surface shadow-panel", className)} {...rest} />;
}

export function PanelHeader({
  title,
  description,
  action,
  className,
}: {
  title: React.ReactNode;
  description?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <header className={cn("flex items-center justify-between gap-4 border-b border-border px-4 py-3 sm:px-5", className)}>
      <div className="min-w-0">
        <h2 className="text-sm font-semibold text-fg">{title}</h2>
        {description && <p className="mt-0.5 text-[13px] text-fg-muted">{description}</p>}
      </div>
      {action}
    </header>
  );
}

// ---------------------------------------------------------------------------
// Toggle — switch control, and the labelled row most settings use it in.
// ---------------------------------------------------------------------------

export function Toggle({
  checked,
  onChange,
  disabled,
  labelledBy,
  describedBy,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  labelledBy?: string;
  describedBy?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        "relative inline-flex h-6 w-10 shrink-0 cursor-pointer items-center rounded-full transition-colors disabled:cursor-not-allowed",
        checked ? "bg-accent" : "bg-surface-hover",
      )}
    >
      <span
        className={cn(
          "inline-block size-[18px] rounded-full shadow transition-transform",
          checked ? "translate-x-[19px] bg-accent-ink" : "translate-x-[3px] bg-fg-muted",
        )}
      />
    </button>
  );
}

export function ToggleRow({
  label,
  description,
  checked,
  onChange,
  disabled,
  className,
}: {
  label: React.ReactNode;
  description?: React.ReactNode;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  className?: string;
}) {
  const labelId = React.useId();
  const descId = React.useId();
  return (
    <label
      className={cn(
        "flex min-h-11 items-center justify-between gap-4",
        disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer",
        className,
      )}
    >
      <span className="min-w-0">
        <span id={labelId} className="block text-sm font-medium text-fg">
          {label}
        </span>
        {description && (
          <span id={descId} className="mt-0.5 block text-[13px] text-fg-muted">
            {description}
          </span>
        )}
      </span>
      <Toggle
        checked={checked}
        onChange={onChange}
        disabled={disabled}
        labelledBy={labelId}
        describedBy={description ? descId : undefined}
      />
    </label>
  );
}

// ---------------------------------------------------------------------------
// Segmented — a compact either/or control.
// ---------------------------------------------------------------------------

export function Segmented<T extends string>({
  label,
  value,
  onChange,
  options,
  className,
}: {
  label: string;
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: React.ReactNode }[];
  className?: string;
}) {
  return (
    <div role="group" aria-label={label} className={cn("inline-flex rounded-md border border-border bg-bg p-0.5", className)}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={o.value === value}
          onClick={() => onChange(o.value)}
          className={cn(
            "inline-flex h-9 flex-1 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-[6px] px-2.5 text-[13px] font-medium transition-colors sm:h-7",
            TOUCH_TARGET,
            o.value === value ? "bg-surface-hover text-fg" : "text-fg-muted hover:text-fg",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// TextField — label, input, hint and on-blur validation wired together.
// ---------------------------------------------------------------------------

export function TextField({
  label,
  hint,
  value,
  onChange,
  validate,
  secret,
  mono,
  placeholder,
  type = "text",
  inputMode,
}: {
  label: React.ReactNode;
  hint?: React.ReactNode;
  value: string;
  onChange: (v: string) => void;
  /** Returns an error message for a bad value. Shown after the field is left. */
  validate?: (v: string) => string | null;
  secret?: boolean;
  mono?: boolean;
  placeholder?: string;
  type?: string;
  inputMode?: React.HTMLAttributes<HTMLInputElement>["inputMode"];
}) {
  const id = React.useId();
  const [touched, setTouched] = React.useState(false);
  const [revealed, setRevealed] = React.useState(false);
  const error = touched && value ? (validate?.(value) ?? null) : null;

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium text-fg">
        {label}
      </label>
      <div className="relative">
        <input
          id={id}
          type={secret && !revealed ? "password" : type}
          value={value}
          inputMode={inputMode}
          placeholder={placeholder}
          autoComplete="off"
          spellCheck={false}
          aria-invalid={error ? true : undefined}
          aria-describedby={`${id}-note`}
          onChange={(e) => onChange(e.target.value)}
          onBlur={() => setTouched(true)}
          className={cn(
            "h-11 w-full rounded-md border border-border bg-bg px-3 text-base text-fg outline-none transition-colors sm:h-9 sm:text-sm",
            "placeholder:text-fg-subtle hover:border-border-strong focus:border-accent focus:ring-1 focus:ring-accent",
            "aria-[invalid=true]:border-danger aria-[invalid=true]:focus:ring-danger",
            (mono || secret) && "font-mono sm:text-[13px]",
            secret && "pr-11",
          )}
        />
        {secret && (
          <button
            type="button"
            onClick={() => setRevealed((r) => !r)}
            aria-label={revealed ? "Hide value" : "Show value"}
            className="absolute inset-y-0 right-0 flex w-11 cursor-pointer items-center justify-center rounded-md text-fg-subtle transition-colors hover:text-fg"
          >
            {revealed ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
          </button>
        )}
      </div>
      <p id={`${id}-note`} role={error ? "alert" : undefined} className={cn("text-[13px]", error ? "text-danger" : "text-fg-muted")}>
        {error ?? hint}
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Status bits — dot, spinner, skeleton.
// ---------------------------------------------------------------------------

export type Tone = "live" | "warn" | "danger" | "neutral";

export const TONE_TEXT: Record<Tone, string> = {
  live: "text-accent",
  warn: "text-warn",
  danger: "text-danger",
  neutral: "text-fg-subtle",
};

export function StatusDot({ tone = "neutral", pulse, className }: { tone?: Tone; pulse?: boolean; className?: string }) {
  return (
    <span className={cn("relative inline-block size-2 shrink-0 rounded-full bg-current", TONE_TEXT[tone], pulse && "pulse-dot", className)} />
  );
}

export function Spinner({ className }: { className?: string }) {
  return (
    <svg className={cn("size-4 animate-spin", className)} viewBox="0 0 24 24" fill="none" aria-hidden>
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeOpacity="0.25" strokeWidth="3" />
      <path d="M12 2a10 10 0 0 1 10 10" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("shimmer rounded-md", className)} />;
}

// ---------------------------------------------------------------------------
// Banner — an inline message with an optional action.
// ---------------------------------------------------------------------------

export function Banner({
  tone,
  icon,
  title,
  children,
  action,
  className,
}: {
  tone: "warn" | "danger";
  icon: React.ReactNode;
  title: React.ReactNode;
  children?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      role="alert"
      className={cn(
        "flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:gap-4 sm:px-5",
        tone === "danger" ? "bg-danger/10" : "bg-warn/10",
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 gap-3">
        <span className={cn("mt-0.5 shrink-0 [&_svg]:size-4", TONE_TEXT[tone])}>{icon}</span>
        <div className="min-w-0">
          <p className="text-sm font-medium text-fg">{title}</p>
          {children && <p className="mt-0.5 break-words text-[13px] text-fg-muted">{children}</p>}
        </div>
      </div>
      {action}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ServerError — full-page fallback when the backend can't be reached.
// ---------------------------------------------------------------------------

export function ServerError({ onRetry, message }: { onRetry: () => void; message?: string }) {
  return (
    <div className="flex min-h-[60dvh] flex-col items-center justify-center gap-4 px-6 text-center">
      <ServerCrash className="size-6 text-fg-subtle" strokeWidth={1.75} />
      <div>
        <h1 className="text-base font-semibold text-fg">Can't reach the server</h1>
        <p className="mt-1 max-w-sm text-sm text-fg-muted">{message ?? "Check your connection, then try again."}</p>
      </div>
      <Button variant="secondary" onClick={onRetry}>
        <RotateCw />
        Try again
      </Button>
    </div>
  );
}
