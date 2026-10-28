import Link from "next/link";

import { DeliveryBadge } from "@/components/badges.tsx";
import { Card, Empty, Mono, PageHeader, Pager, Time, tableStyles as t } from "@/components/ui.tsx";
import { orFail, requireSession } from "@/lib/session.ts";

export default async function EventPage({ params, searchParams }: { params: Promise<{ id: string }>; searchParams: Promise<{ cursor?: string }> }) {
  const [{ id }, { cursor }] = await Promise.all([params, searchParams]);
  const { api } = await requireSession();
  const [event, deliveries] = await Promise.all([api.getEvent(id), api.listEventDeliveries(id, cursor)]).catch(orFail);
  // One request per distinct endpoint, in parallel; an event fans out to a
  // handful of endpoints, not hundreds.
  const endpointIDs = [...new Set(deliveries.data.map((d) => d.endpoint_id))];
  const endpoints = new Map((await Promise.all(endpointIDs.map((e) => api.getEndpoint(e)))).map((e) => [e.id, e]));

  return (
    <>
      <PageHeader
        title={<span className="font-mono">{event.event_type}</span>}
        description={
          <>
            Published <Time iso={event.created_at} /> · <Mono>{event.id}</Mono>
            {event.idempotency_key && (
              <>
                {" "}· idempotency key <Mono>{event.idempotency_key}</Mono>
              </>
            )}
          </>
        }
      />
      <div className="grid gap-6 lg:grid-cols-5">
        <div className="lg:col-span-3">
          <Card title="Deliveries">
            {deliveries.data.length === 0 ? (
              <Empty>No endpoint was subscribed to this event type when it was published.</Empty>
            ) : (
              <table className={t.table}>
                <thead className={t.head}>
                  <tr>
                    <th className={t.th}>Endpoint</th>
                    <th className={t.th}>Status</th>
                    <th className={`${t.th} text-right`}>Attempts</th>
                  </tr>
                </thead>
                <tbody>
                  {deliveries.data.map((d) => (
                    <tr key={d.id} className={t.row}>
                      <td className={`${t.td} max-w-0 w-full`}>
                        <Link href={`/deliveries/${d.id}`} className="block truncate font-mono text-xs text-indigo-600 hover:underline dark:text-indigo-400">
                          {endpoints.get(d.endpoint_id)?.url ?? d.endpoint_id}
                        </Link>
                        {d.status === "pending" && d.next_attempt_at && (
                          <span className="text-xs text-zinc-500">
                            next attempt <Time iso={d.next_attempt_at} />
                          </span>
                        )}
                      </td>
                      <td className={t.td}>
                        <DeliveryBadge status={d.status} />
                      </td>
                      <td className={`${t.td} text-right tabular-nums`}>{d.attempt_count}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            <Pager path={`/events/${id}`} query={{ cursor }} nextCursor={deliveries.next_cursor} />
          </Card>
        </div>
        <div className="lg:col-span-2">
          <Card title="Payload">
            <pre className="overflow-x-auto p-4 font-mono text-xs leading-relaxed">{JSON.stringify(event.payload, null, 2)}</pre>
          </Card>
        </div>
      </div>
    </>
  );
}
