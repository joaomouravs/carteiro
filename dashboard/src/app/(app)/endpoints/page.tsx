import type { Metadata } from "next";
import Link from "next/link";

import { EndpointBadge } from "@/components/badges.tsx";
import { Card, Empty, Mono, PageHeader, Pager, Time, tableStyles as t } from "@/components/ui.tsx";
import { orFail, requireSession } from "@/lib/session.ts";

export const metadata: Metadata = { title: "Endpoints" };

export default async function EndpointsPage({ searchParams }: { searchParams: Promise<{ cursor?: string }> }) {
  const { cursor } = await searchParams;
  const { api } = await requireSession();
  const page = await api.listEndpoints(cursor).catch(orFail);

  return (
    <>
      <PageHeader title="Endpoints" description="Where your events go. A failing endpoint is disabled after 10 failures over 5 days, or at once on 410 Gone." />
      <Card>
        {page.data.length === 0 ? (
          <Empty>No endpoints yet. Create one with POST /v1/endpoints.</Empty>
        ) : (
          <table className={t.table}>
            <thead className={t.head}>
              <tr>
                <th className={t.th}>URL</th>
                <th className={t.th}>Status</th>
                <th className={t.th}>Event types</th>
                <th className={`${t.th} text-right`}>Failing</th>
              </tr>
            </thead>
            <tbody>
              {page.data.map((e) => (
                <tr key={e.id} className={t.row}>
                  <td className={`${t.td} max-w-0 w-full`}>
                    <Link href={`/endpoints/${e.id}`} className="block truncate font-mono text-xs text-indigo-600 hover:underline dark:text-indigo-400">
                      {e.url}
                    </Link>
                    {e.description && <span className="block truncate text-xs text-zinc-500">{e.description}</span>}
                  </td>
                  <td className={t.td}>
                    <EndpointBadge status={e.status} reason={e.disabled_reason} />
                  </td>
                  <td className={t.td}>{e.event_types.length === 0 ? <span className="text-zinc-500">all</span> : <Mono>{e.event_types.join(", ")}</Mono>}</td>
                  <td className={`${t.td} text-right whitespace-nowrap text-zinc-500`}>
                    {e.failing_since ? (
                      <span className="text-amber-700 dark:text-amber-400">
                        {e.consecutive_failures}× since <Time iso={e.failing_since} />
                      </span>
                    ) : (
                      "—"
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <Pager path="/endpoints" query={{ cursor }} nextCursor={page.next_cursor} />
      </Card>
    </>
  );
}
