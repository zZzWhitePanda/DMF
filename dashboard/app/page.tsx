"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Ban, KeyRound, MonitorSmartphone } from "lucide-react";
import { auth, license, MeResponse } from "@/lib/api";
import { computeHWID } from "@/lib/hwid";
import { fmtCountdown } from "@/lib/utils";
import { Button, Logo, ServerError, Spinner, TextField } from "@/components/ui";

export default function Home() {
  const router = useRouter();
  const [state, setState] = useState<MeResponse | null>(null);
  const [hwid, setHwid] = useState("");
  const [failed, setFailed] = useState(false);

  const load = useCallback(async () => {
    setFailed(false);
    try {
      setHwid(await computeHWID());
      const me = await auth.me();
      setState(me);
      if (me.signed_in && (me.license_status === "ok" || me.license_status === "admin")) {
        router.replace("/dashboard");
      }
    } catch {
      setFailed(true);
    }
  }, [router]);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <main className="relative flex min-h-dvh items-center justify-center overflow-hidden p-6">
      <Skyline />
      <div className="relative z-10 w-full max-w-sm">
        {failed ? (
          <ServerError onRetry={load} />
        ) : !state ? (
          <CenteredSpinner />
        ) : !state.signed_in ? (
          <SignIn hwid={hwid} />
        ) : state.license_status === "none" ? (
          <StatusCard
            icon={<KeyRound />}
            title="Enter your license key"
            body={
              <>
                Signed in as <span className="text-fg">{state.user?.email}</span>. The key locks to this account, so
                make sure it's the right one.
              </>
            }
          >
            <RedeemKey />
          </StatusCard>
        ) : state.license_status === "inactive" ? (
          <StatusCard
            icon={<Ban />}
            title="License expired"
            body="This license is no longer active. Enter a new key to carry on."
          >
            <RedeemKey />
          </StatusCard>
        ) : state.license_status === "hwid_mismatch" ? (
          <HwidMismatch resetAt={state.reset_available_at ?? 0} />
        ) : (
          <CenteredSpinner />
        )}
      </div>
    </main>
  );
}

// Earnings-chart bars run along the bottom edge, the same shape the farm
// draws once it's live. Heights are a fixed sum of sines so the static export
// and the client render agree.
function Skyline() {
  const bars = Array.from({ length: 56 }, (_, i) => {
    const wave = Math.sin(i * 0.31) + Math.sin(i * 0.83 + 1) * 0.6 + Math.sin(i * 2.1) * 0.35;
    return Math.round(16 + (wave + 2) * 21);
  });
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-x-0 bottom-0 flex h-[38dvh] items-end gap-[3px] opacity-70 [mask-image:linear-gradient(to_top,black,transparent)]"
    >
      {bars.map((h, i) => (
        <div key={i} className="min-w-0 flex-1 rounded-t bg-surface-hover" style={{ height: `${h}%` }} />
      ))}
    </div>
  );
}

function Brand({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center text-center">
      <Logo className="size-12" />
      <h1 className="mt-6 text-[28px] font-semibold leading-tight tracking-tight">Dank Memer Farm</h1>
      {children}
    </div>
  );
}

function SignIn({ hwid }: { hwid: string }) {
  return (
    <Brand>
      <p className="mt-2 text-balance text-[15px] text-fg-muted">
        Grinds on the server, day and night. Run it from anywhere.
      </p>
      <a
        href={auth.signInUrl(hwid)}
        className="mt-8 flex h-12 w-full cursor-pointer items-center justify-center gap-2.5 rounded-md bg-fg text-[15px] font-medium text-bg transition-opacity hover:opacity-90 active:opacity-80"
      >
        <GoogleIcon />
        Continue with Google
      </a>
      <p className="mt-4 text-[13px] text-fg-subtle">Licensed accounts only.</p>
    </Brand>
  );
}

function StatusCard({
  icon,
  title,
  body,
  children,
}: {
  icon: React.ReactNode;
  title: string;
  body: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-center text-center">
      <span className="flex size-12 items-center justify-center rounded-full border border-border-strong bg-surface text-fg-muted [&_svg]:size-5">
        {icon}
      </span>
      <h1 className="mt-5 text-xl font-semibold tracking-tight">{title}</h1>
      <p className="mt-2 text-sm leading-relaxed text-fg-muted">{body}</p>
      {children}
      <button
        onClick={() => auth.logout().then(() => location.reload())}
        className="mt-3 h-11 cursor-pointer rounded-md px-3 text-sm text-fg-muted transition-colors hover:text-fg"
      >
        Sign out
      </button>
    </div>
  );
}

function RedeemKey() {
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function redeem() {
    setBusy(true);
    setError("");
    try {
      await license.redeem(key);
      location.href = "/dashboard/";
    } catch (e) {
      setError((e as Error).message);
      setBusy(false);
    }
  }

  return (
    <form
      className="mt-6 grid w-full gap-3 text-left"
      onSubmit={(e) => {
        e.preventDefault();
        redeem();
      }}
    >
      <TextField label="License key" mono placeholder="DMF-XXXX-XXXX-XXXX-XXXX" value={key} onChange={setKey} />
      {error && (
        <p role="alert" className="text-[13px] text-danger">
          {error}
        </p>
      )}
      <Button type="submit" size="lg" className="w-full" loading={busy} disabled={!key.trim()}>
        Unlock my farm
      </Button>
    </form>
  );
}

function HwidMismatch({ resetAt }: { resetAt: number }) {
  const [now, setNow] = useState(() => Date.now() / 1000);
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now() / 1000), 1000);
    return () => clearInterval(t);
  }, []);
  const canReset = now >= resetAt;
  return (
    <StatusCard
      icon={<MonitorSmartphone />}
      title="This is a new device"
      body={
        canReset ? (
          "Your license is tied to a different device. Move it here to continue."
        ) : (
          <>
            Your license is tied to a different device. You can move it here in{" "}
            <span className="font-mono tabular-nums text-fg">{fmtCountdown(resetAt - now)}</span>.
          </>
        )
      }
    >
      <Button
        size="lg"
        className="mt-6 w-full"
        disabled={!canReset}
        onClick={async () => {
          await fetch("/api/license/reset-hwid", { method: "POST", credentials: "include" });
          location.reload();
        }}
      >
        Move license to this device
      </Button>
    </StatusCard>
  );
}

function CenteredSpinner() {
  return (
    <div className="flex items-center justify-center">
      <Spinner className="size-6 text-fg-muted" />
    </div>
  );
}

function GoogleIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 48 48" aria-hidden="true">
      <path
        fill="#FFC107"
        d="M43.6 20.5h-1.9V20H24v8h11.3c-1.6 4.6-6 8-11.3 8-6.6 0-12-5.4-12-12s5.4-12 12-12c3.1 0 5.9 1.2 8 3.1l5.7-5.7C34 6.1 29.3 4 24 4 12.9 4 4 12.9 4 24s8.9 20 20 20 20-8.9 20-20c0-1.3-.1-2.3-.4-3.5z"
      />
      <path
        fill="#FF3D00"
        d="m6.3 14.7 6.6 4.8c1.8-4.4 6.1-7.5 11.1-7.5 3.1 0 5.9 1.2 8 3.1l5.7-5.7C34 6.1 29.3 4 24 4 16.3 4 9.7 8.3 6.3 14.7z"
      />
      <path
        fill="#4CAF50"
        d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2c-2 1.4-4.5 2.4-7.2 2.4-5.2 0-9.6-3.3-11.3-8l-6.5 5C9.5 39.6 16.2 44 24 44z"
      />
      <path
        fill="#1976D2"
        d="M43.6 20.5h-1.9V20H24v8h11.3c-.8 2.2-2.2 4.1-4.1 5.6l6.2 5.2c-.4.4 6.6-4.8 6.6-14.8 0-1.3-.1-2.3-.4-3.5z"
      />
    </svg>
  );
}
