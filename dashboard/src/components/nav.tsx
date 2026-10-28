"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const links = [
  { href: "/events", label: "Events", match: ["/events", "/deliveries"] },
  { href: "/endpoints", label: "Endpoints", match: ["/endpoints"] },
];

export function Nav() {
  const path = usePathname();
  return (
    <nav className="flex gap-1 sm:flex-col">
      {links.map((l) => {
        const active = l.match.some((m) => path.startsWith(m));
        return (
          <Link
            key={l.href}
            href={l.href}
            aria-current={active ? "page" : undefined}
            className={`rounded-lg px-3 py-2 text-sm font-medium ${
              active
                ? "bg-zinc-200/70 text-zinc-900 dark:bg-zinc-800 dark:text-zinc-100"
                : "text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800/60 dark:hover:text-zinc-100"
            }`}
          >
            {l.label}
          </Link>
        );
      })}
    </nav>
  );
}
