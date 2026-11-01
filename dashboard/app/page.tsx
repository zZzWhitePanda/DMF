"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { auth, MeResponse } from "@/lib/api";
import { computeHWID } from "@/lib/hwid";
import { cn } from "@/lib/utils";

// Landing page. Routes the user based on their auth + license status:
//   signed_in=false            → "Sign in with Google" button
//   license_status=none        → "No license on this account" dead end
//   license_status=inactive    → "Your license has been revoked"
//   license_status=hwid_mismatch → "Reset device" button (gated to 24h)
//   license_status=ok | admin  → redirect to /dashboard
export default function Home() {
  const router = useRouter();
  const [state, setState] = useState<MeResponse | null>(null);
  const [hwid, setHwid] = useState("");

  useEffect(() => {
    (async () => {
      const h = await computeHWID();
      setHwid(h);
      const me = await auth.me();
      setState(me);
      if (me.signed_in && (me.license_status === "ok" || me.license_status === "admin")) {
        router.replace("/dashboard");
      }
    })();
  }, [router]);

  if (!state) return <CenteredSpinner />;

  if (!state.signed_in) {
    return (
      <Shell>
        <h1 className="text-3xl font-semibold text-foreground">memeforge</h1>
        <p className="text-muted">Sign in to control your Dank Memer grinder.</p>
        <a
          href={auth.signInUrl(hwid)}
          className={cn(
            "inline-flex items-center gap-3 rounded-lg bg-accent px-6 py-3 font-medium text-accent-foreground",
            "transition hover:brightness-110",
          )}
        >
          <GoogleIcon />
          Sign in with Google
        </a>
      </Shell>
    );
  }

  // Signed in but not licensed — route by status.
  const status = state.license_status;
  if (status === "none") {
    return (
      <Shell>
        <h1 className="text-2xl font-semibold">no license on this account</h1>
        <p className="text-muted">
          Signed in as <span className="text-foreground">{state.user?.email}</span>. There's no active license
          bound to this Google account. Contact an admin to get one minted for you.
        </p>
        <button
          onClick={() => auth.logout().then(() => location.reload())}
          className="text-sm text-muted underline"
        >
          Sign out
        </button>
      </Shell>
    );
  }
  if (status === "inactive") {
    return (
      <Shell>
        <h1 className="text-2xl font-semibold text-error">license revoked or expired</h1>
        <p className="text-muted">Your license is no longer active. Contact an admin.</p>
        <button
          onClick={() => auth.logout().then(() => location.reload())}
          className="text-sm text-muted underline"
        >
          Sign out
        </button>
      </Shell>
    );
  }
  if (status === "hwid_mismatch") {
    const resetAt = state.reset_available_at ?? 0;
    const canReset = Date.now() / 1000 >= resetAt;
    return (
      <Shell>
        <h1 className="text-2xl font-semibold">device mismatch</h1>
        <p className="text-muted">
          This license is bound to a different device. Reset it to this device?{" "}
          {canReset ? (
            "You can reset now."
          ) : (
            <>You can reset again in <Countdown target={resetAt} />.</>
          )}
        </p>
        <button
          disabled={!canReset}
          onClick={async () => {
            await fetch("/api/license/reset-hwid", { method: "POST", credentials: "include" });
            location.reload();
          }}
          className={cn(
            "rounded-lg px-4 py-2 font-medium",
            canReset
              ? "bg-accent text-accent-foreground hover:brightness-110"
              : "bg-border text-muted cursor-not-allowed",
          )}
        >
          Reset device
        </button>
      </Shell>
    );
  }

  return <CenteredSpinner />;
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <main className="flex min-h-dvh items-center justify-center p-6">
      <div className="flex max-w-md flex-col items-start gap-4 rounded-xl border border-border bg-card p-8">
        {children}
      </div>
    </main>
  );
}

function CenteredSpinner() {
  return (
    <main className="flex min-h-dvh items-center justify-center">
      <div className="h-8 w-8 animate-spin rounded-full border-2 border-muted border-t-accent" />
    </main>
  );
}

function GoogleIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 48 48" aria-hidden="true">
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

function Countdown({ target }: { target: number }) {
  const [remaining, setRemaining] = useState(target - Math.floor(Date.now() / 1000));
  useEffect(() => {
    const t = setInterval(() => setRemaining(target - Math.floor(Date.now() / 1000)), 1000);
    return () => clearInterval(t);
  }, [target]);
  if (remaining <= 0) return <>now</>;
  const h = Math.floor(remaining / 3600);
  const m = Math.floor((remaining % 3600) / 60);
  const s = remaining % 60;
  return (
    <span className="font-mono">
      {h}h {m}m {s}s
    </span>
  );
}
