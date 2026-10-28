import type { Metadata } from "next";
import Link from "next/link";

import { Card, Empty, Mono, PageHeader, Pager, Time, buttonStyles, tableStyles as t } from "@/components/ui.tsx";
import { shortID } from "@/lib/format.ts";
import { orFail, requireSession } from "@/lib/session.ts";

export const metadata: Metadata = { title: "Events" };

type Search = { cursor?: string; type?: string };

export default async function EventsPage({ searchParams }: { searchParams: Promise<Search> }) {
  const { cursor, type } = await searchParams;
  const { api } = await requireSession();
  const page = await api.listEvents({ cursor, eventType: type || undefined }).catch(orFail);

  return (
    <>
      <PageHeader title="Events" description="Newest first. Open one to see where it was delivered and how each attempt went." />
      <Card
        title={type ? <>Type <Mono>{type}</Mono></> : "All events"}
        actions={
          <form className="flex w-full items-center gap-2 sm:w-auto" role="search">
            <label htmlFor="type" className="sr-only">
              Event type
            </label>
            <input
              id="type"
              name="type"
              defaultValue={type}
              placeholder="Filter by type, e.g. invoice.paid"
              className="w-full min-w-0 rounded-lg sm:w-64 border-0 bg-white px-3 py-1.5 font-mono text-xs ring-1 ring-inset ring-zinc-300 focus:ring-2 focus:ring-indigo-600 dark:bg-zinc-900 dark:ring-zinc-700"
            />
            <button type="submit" className={buttonStyles.secondary}>
              Filter
            </button>
          </form>
        }
      >
        {page.data.length === 0 ? (
          <Empty>{type ? `No events of type ${type}.` : "No events yet. Publish one with POST /v1/events."}</Empty>
        ) : (
          <table className={t.table}>
            <thead className={t.head}>
              <tr>
                <th className={t.th}>Type</th>
                <th className={`${t.th} hidden sm:table-cell`}>Event</th>
                <th className={`${t.th} hidden md:table-cell`}>Idempotency key</th>
                <th className={`${t.th} text-right`}>Published</th>
              </tr>
            </thead>
            <tbody>
              {page.data.map((e) => (
                <tr key={e.id} className={t.row}>
                  <td className={t.td}>
                    <Link href={`/events/${e.id}`} className="font-mono text-sm font-medium text-indigo-600 hover:underline dark:text-indigo-400">
                      {e.event_type}
                    </Link>
                  </td>
                  <td className={`${t.td} hidden sm:table-cell`}>
                    <Mono>…{shortID(e.id)}</Mono>
                  </td>
                  <td className={`${t.td} hidden md:table-cell`}>{e.idempotency_key ? <Mono>{e.idempotency_key}</Mono> : <span className="text-zinc-400">—</span>}</td>
                  <td className={`${t.td} text-right text-zinc-500`}>
                    <Time iso={e.created_at} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <Pager path="/events" query={{ type, cursor }} nextCursor={page.next_cursor} />
      </Card>
    </>
  );
}
