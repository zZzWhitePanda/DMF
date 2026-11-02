"use client";

import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { LogOut } from "lucide-react";
import { auth } from "@/lib/api";
import type { User } from "@/lib/use-user";
import { cn } from "@/lib/utils";
import { Logo, Skeleton } from "@/components/ui";

type Page = "farm" | "settings" | "admin";

const NAV: { page: Page; label: string; href: string }[] = [
  { page: "farm", label: "Farm", href: "/dashboard/" },
  { page: "settings", label: "Settings", href: "/settings/" },
];

export function AppShell({
  page,
  user,
  children,
}: {
  page: Page;
  user: User | null;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-dvh flex-col">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-3 focus:z-50 focus:rounded-md focus:bg-surface-raised focus:px-3 focus:py-2"
      >
        Skip to content
      </a>
      <header className="sticky top-0 z-30 border-b border-border bg-bg">
        <div className="mx-auto flex h-14 w-full max-w-6xl items-center gap-3 px-4 sm:gap-8 sm:px-6">
          <a
            href="/dashboard/"
            aria-label="Dank Memer Farm home"
            className="-mx-2 flex h-11 shrink-0 items-center gap-2.5 rounded-md px-2"
          >
            <Logo />
            <span className="hidden text-[15px] font-semibold tracking-tight sm:inline">Dank Memer Farm</span>
          </a>
          <nav aria-label="Main" className="flex h-full items-stretch gap-1">
            {NAV.map((item) => (
              <NavItem key={item.page} {...item} active={item.page === page} />
            ))}
            {user?.isAdmin && <NavItem label="Admin" href="/admin/" active={page === "admin"} />}
          </nav>
          <div className="ml-auto">{user ? <UserMenu user={user} /> : <Skeleton className="size-8 rounded-full" />}</div>
        </div>
      </header>
      <main id="main" className="mx-auto w-full max-w-6xl flex-1 px-4 py-5 sm:px-6 sm:py-8">
        {children}
      </main>
    </div>
  );
}

function NavItem({ label, href, active }: { label: string; href: string; active: boolean }) {
  return (
    <a
      href={href}
      aria-current={active ? "page" : undefined}
      className={cn(
        "relative flex items-center rounded-sm px-3 text-sm font-medium transition-colors",
        active
          ? "text-fg after:absolute after:inset-x-3 after:-bottom-px after:h-0.5 after:bg-fg"
          : "text-fg-muted hover:text-fg",
      )}
    >
      {label}
    </a>
  );
}

function UserMenu({ user }: { user: User }) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        aria-label="Account menu"
        className="flex size-11 cursor-pointer items-center justify-center rounded-full sm:size-9"
      >
        <Avatar user={user} />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={6}
          className="z-40 w-64 rounded-lg border border-border-strong bg-surface-raised p-1 shadow-popover data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95"
        >
          <div className="flex items-center gap-3 px-2.5 py-2">
            <Avatar user={user} />
            <div className="min-w-0">
              <div className="truncate text-sm font-medium text-fg">{user.name || user.email}</div>
              <div className="truncate text-[13px] text-fg-muted">{user.email}</div>
            </div>
          </div>
          <DropdownMenu.Separator className="my-1 h-px bg-border" />
          <DropdownMenu.Item
            onSelect={() => auth.logout().then(() => (location.href = "/"))}
            className="flex h-10 cursor-pointer items-center gap-2.5 rounded-md px-2.5 text-sm text-fg-muted outline-none data-[highlighted]:bg-surface-hover data-[highlighted]:text-fg sm:h-9"
          >
            <LogOut className="size-4" />
            Sign out
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function Avatar({ user }: { user: User }) {
  if (user.picture) {
    return (
      <img
        src={user.picture}
        alt=""
        referrerPolicy="no-referrer"
        className="size-8 shrink-0 rounded-full ring-1 ring-border-strong"
      />
    );
  }
  return (
    <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-surface-hover text-[13px] font-semibold text-fg ring-1 ring-border-strong">
      {(user.name || user.email).charAt(0).toUpperCase()}
    </span>
  );
}
