import type { Metadata } from "next";
import Link from "next/link";

import { replayDelivery } from "@/app/(app)/actions.ts";
import { ActionButton } from "@/components/actions.tsx";
import { DeliveryBadge, EndpointBadge, HTTPStatus } from "@/components/badges.tsx";
import { Card, Empty, Mono, PageHeader, Pager, Time } from "@/components/ui.tsx";
import { duration } from "@/lib/format.ts";
import { orFail, requireSession } from "@/lib/session.ts";

export const metadata: Metadata = { title: "Delivery" };

export default async function DeliveryPage({ params, searchParams }: { params: Promise<{ id: string }>; searchParams: Promise<{ cursor?: string }> }) {
  const [{ id }, { cursor }] = await Promise.all([params, searchParams]);
  const { api } = await requireSession();
  const [delivery, attempts] = await Promise.all([api.getDelivery(id), api.listAttempts(id, cursor)]).catch(orFail);
  const [event, endpoint] = await Promise.all([api.getEvent(delivery.event_id), api.getEndpoint(delivery.endpoint_id)]).catch(orFail);

  const queued = delivery.status === "pending" || delivery.status === "in_flight";
  const disabledReason = queued
    ? "Already queued: Carteiro is still trying."
    : endpoint.status !== "active"
      ? "The endpoint is disabled. Enable it first."
      : undefined;

  return (
    <>
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            Delivery <DeliveryBadge status={delivery.status} />
          </span>
        }
        description={
          <>
            <Link href={`/events/${event.id}`} className="font-mono text-indigo-600 hover:underline dark:text-indigo-400">
              {event.event_type}
            </Link>{" "}
            to{" "}
            <Link href={`/endpoints/${endpoint.id}`} className="font-mono text-indigo-600 hover:underline dark:text-indigo-400">
              {endpoint.url}
            </Link>
          </>
        }
        actions={
          <ActionButton
            action={replayDelivery.bind(null, delivery.id)}
            label="Replay"
            pendingLabel="Queuing…"
            disabled={disabledReason !== undefined}
            disabledReason={disabledReason}
          />
        }
      />

      <dl className="mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
        {[
          ["Attempts", <span key="a" className="tabular-nums">{delivery.attempt_count}</span>],
          ["Next attempt", delivery.next_attempt_at ? <Time key="n" iso={delivery.next_attempt_at} /> : "—"],
          ["Endpoint", <EndpointBadge key="e" status={endpoint.status} reason={endpoint.disabled_reason} />],
          ["Delivery id", <Mono key="i">{delivery.id}</Mono>],
        ].map(([label, value]) => (
          <div key={String(label)} className="rounded-xl border border-zinc-200 bg-white px-4 py-3 dark:border-zinc-800 dark:bg-zinc-900">
            <dt className="text-xs text-zinc-500">{label}</dt>
            <dd className="mt-1 truncate text-sm font-medium">{value}</dd>
          </div>
        ))}
      </dl>

      <Card title="Attempts, newest first">
        {attempts.data.length === 0 ? (
          <Empty>No attempt yet: it is waiting for a worker.</Empty>
        ) : (
          <ol className="divide-y divide-zinc-100 dark:divide-zinc-800">
            {attempts.data.map((a) => (
              <li key={a.id} className="px-4 py-3">
                <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
                  <span className="font-medium tabular-nums">#{a.attempt_number}</span>
                  <HTTPStatus code={a.response_status} />
                  <span className="tabular-nums text-zinc-500">{duration(a.duration_ms)}</span>
                  <span className="ml-auto text-zinc-500">
                    <Time iso={a.started_at} />
                  </span>
                </div>
                {a.error && <p className="mt-1 font-mono text-xs break-all text-rose-700 dark:text-rose-400">{a.error}</p>}
                {a.response_body && (
                  <details className="mt-2">
                    <summary className="cursor-pointer text-xs text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100">Response body</summary>
                    <pre className="mt-2 max-h-64 overflow-auto rounded-lg bg-zinc-50 p-3 font-mono text-xs break-all whitespace-pre-wrap dark:bg-zinc-950">
                      {a.response_body}
                    </pre>
                  </details>
                )}
              </li>
            ))}
          </ol>
        )}
        <Pager path={`/deliveries/${id}`} query={{ cursor }} nextCursor={attempts.next_cursor} />
      </Card>
    </>
  );
}
