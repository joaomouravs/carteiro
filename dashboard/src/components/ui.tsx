import Link from "next/link";
import type { ReactNode } from "react";

import { absoluteTime, relativeTime } from "@/lib/format.ts";

export function PageHeader({ title, description, actions }: { title: ReactNode; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        <h1 className="truncate text-xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Card({ title, children, actions }: { title?: ReactNode; children: ReactNode; actions?: ReactNode }) {
  return (
    <section className="rounded-xl border border-zinc-200 bg-white shadow-xs dark:border-zinc-800 dark:bg-zinc-900">
      {title && (
        <header className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
          <h2 className="text-sm font-semibold">{title}</h2>
          {actions}
        </header>
      )}
      {/* Wide tables scroll inside the card instead of widening the page. */}
      <div className="overflow-x-auto">{children}</div>
    </section>
  );
}

// Time shows a relative time, with the exact UTC time on hover.
export function Time({ iso }: { iso: string }) {
  return (
    <time dateTime={iso} title={absoluteTime(iso)} className="whitespace-nowrap">
      {relativeTime(iso)}
    </time>
  );
}

export function Mono({ children }: { children: ReactNode }) {
  return <span className="font-mono text-xs text-zinc-600 dark:text-zinc-400">{children}</span>;
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="px-4 py-10 text-center text-sm text-zinc-500">{children}</p>;
}

// Pager links to the next page with Carteiro's opaque cursor, keeping the
// other query parameters.
export function Pager({ path, query, nextCursor }: { path: string; query: Record<string, string | undefined>; nextCursor: string | null }) {
  const href = (cursor?: string) => {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries({ ...query, cursor })) if (v) params.set(k, v);
    const qs = params.toString();
    return qs ? `${path}?${qs}` : path;
  };
  const hasCursor = Boolean(query.cursor);
  if (!nextCursor && !hasCursor) return null;
  return (
    <nav className="flex justify-between border-t border-zinc-200 px-4 py-3 text-sm dark:border-zinc-800">
      {hasCursor ? <Link href={href()} className="text-zinc-600 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100">← Newest</Link> : <span />}
      {nextCursor && <Link href={href(nextCursor)} className="font-medium text-indigo-600 hover:text-indigo-500 dark:text-indigo-400">Older →</Link>}
    </nav>
  );
}

const button = "inline-flex items-center justify-center gap-2 rounded-lg px-3 py-1.5 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50";

export const buttonStyles = {
  primary: `${button} bg-indigo-600 text-white shadow-xs hover:bg-indigo-500 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-indigo-600`,
  secondary: `${button} bg-white text-zinc-900 ring-1 ring-inset ring-zinc-300 hover:bg-zinc-50 dark:bg-zinc-800 dark:text-zinc-100 dark:ring-zinc-700 dark:hover:bg-zinc-700`,
  danger: `${button} bg-rose-600 text-white shadow-xs hover:bg-rose-500`,
};

export const tableStyles = {
  table: "w-full text-left text-sm",
  head: "border-b border-zinc-200 text-xs uppercase tracking-wide text-zinc-500 dark:border-zinc-800",
  th: "px-4 py-2 font-medium",
  row: "border-b border-zinc-100 last:border-0 hover:bg-zinc-50 dark:border-zinc-800/60 dark:hover:bg-zinc-800/40",
  td: "px-4 py-3 align-middle",
};
