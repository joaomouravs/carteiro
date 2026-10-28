import type { ReactNode } from "react";

import { Nav } from "@/components/nav.tsx";
import { requireSession } from "@/lib/session.ts";

export default async function AppLayout({ children }: { children: ReactNode }) {
  const { session } = await requireSession();
  return (
    <div className="mx-auto flex min-h-screen max-w-7xl flex-col sm:flex-row">
      <aside className="flex shrink-0 flex-col gap-6 border-b border-zinc-200 p-4 sm:w-56 sm:border-r sm:border-b-0 dark:border-zinc-800">
        <div>
          <p className="text-lg font-semibold tracking-tight">Carteiro</p>
          <p className="truncate text-xs text-zinc-500" title={session.appName}>
            {session.appName}
          </p>
        </div>
        <Nav />
        <form action="/logout" method="post" className="sm:mt-auto">
          <button type="submit" className="text-sm text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100">
            Sign out
          </button>
        </form>
      </aside>
      <main className="min-w-0 flex-1 p-4 sm:p-8">{children}</main>
    </div>
  );
}
