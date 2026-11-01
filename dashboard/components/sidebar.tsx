"use client";

import { auth } from "@/lib/api";
import { cn } from "@/lib/utils";

// Shared sidebar for the authenticated pages. Lives outside app/ so
// Next.js's page-file export restrictions don't trip when both the
// Farm page and the Settings page import it.
export function Sidebar({
  page,
  me,
}: {
  page: "farm" | "settings";
  me: { email: string; picture: string } | null;
}) {
  return (
    <aside className="flex w-56 flex-col border-r border-border bg-card px-4 py-6">
      <div className="mb-6 flex items-center gap-3">
        <div className="h-6 w-6 rounded bg-accent" />
        <span className="font-semibold">Dank Memer Farm</span>
      </div>
      <nav className="flex flex-col gap-1">
        <NavItem label="Farm" href="/dashboard/" active={page === "farm"} />
        <NavItem label="Settings" href="/settings/" active={page === "settings"} />
      </nav>
      <div className="mt-auto pt-8">
        {me && (
          <div className="flex items-center gap-2 text-xs text-muted">
            {me.picture && <img src={me.picture} alt="" className="h-6 w-6 rounded-full" />}
            <span className="truncate">{me.email}</span>
          </div>
        )}
        <button
          onClick={() => auth.logout().then(() => (location.href = "/"))}
          className="mt-4 text-xs text-muted underline hover:text-foreground"
        >
          Sign out
        </button>
      </div>
    </aside>
  );
}

function NavItem({ label, href, active }: { label: string; href: string; active?: boolean }) {
  return (
    <a
      href={href}
      className={cn(
        "rounded-md px-3 py-2 text-sm",
        active ? "bg-accent/15 text-foreground" : "text-muted hover:bg-border/40 hover:text-foreground",
      )}
    >
      {label}
    </a>
  );
}
