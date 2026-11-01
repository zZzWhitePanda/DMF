"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { auth } from "@/lib/api";

export interface User {
  email: string;
  name: string;
  picture: string;
  isAdmin: boolean;
}

// Gate for signed-in pages. Anyone without a usable license goes back to the
// home page, which explains why. `failed` means the check itself couldn't run.
export function useUser() {
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [failed, setFailed] = useState(false);

  const load = useCallback(async () => {
    setFailed(false);
    try {
      const info = await auth.me();
      if (!info.signed_in || (info.license_status !== "ok" && info.license_status !== "admin")) {
        router.replace("/");
        return;
      }
      setUser({
        email: info.user?.email ?? "",
        name: info.user?.name ?? "",
        picture: info.user?.picture ?? "",
        isAdmin: !!info.user?.is_admin,
      });
    } catch {
      setFailed(true);
    }
  }, [router]);

  useEffect(() => {
    load();
  }, [load]);

  return { user, failed, retry: load };
}
