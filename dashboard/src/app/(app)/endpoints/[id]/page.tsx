import type { Metadata } from "next";
import { enableEndpoint, replayEndpoint, rotateSecret } from "@/app/(app)/actions.ts";
import { ActionButton, ReplayEndpointForm, RotateSecretForm } from "@/components/actions.tsx";
import { EndpointBadge } from "@/components/badges.tsx";
import { Card, Mono, PageHeader, Time } from "@/components/ui.tsx";
import { orFail, requireSession } from "@/lib/session.ts";

export const metadata: Metadata = { title: "Endpoint" };

export default async function EndpointPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const { api } = await requireSession();
  const ep = await api.getEndpoint(id).catch(orFail);
  const disabled = ep.status !== "active";

  return (
    <>
      <PageHeader
        title={<span className="font-mono text-lg">{ep.url}</span>}
        description={ep.description || <Mono>{ep.id}</Mono>}
        actions={<EndpointBadge status={ep.status} reason={ep.disabled_reason} />}
      />
      <div className="grid gap-6 lg:grid-cols-2">
        <Card title="Health">
          <dl className="grid grid-cols-2 gap-4 p-4 text-sm">
            <div>
              <dt className="text-xs text-zinc-500">Failures in a row</dt>
              <dd className="mt-1 font-medium tabular-nums">{ep.consecutive_failures}</dd>
            </div>
            <div>
              <dt className="text-xs text-zinc-500">Failing since</dt>
              <dd className="mt-1 font-medium">{ep.failing_since ? <Time iso={ep.failing_since} /> : "—"}</dd>
            </div>
            <div>
              <dt className="text-xs text-zinc-500">Event types</dt>
              <dd className="mt-1">{ep.event_types.length === 0 ? "all" : <Mono>{ep.event_types.join(", ")}</Mono>}</dd>
            </div>
            <div>
              <dt className="text-xs text-zinc-500">Created</dt>
              <dd className="mt-1">
                <Time iso={ep.created_at} />
              </dd>
            </div>
          </dl>
          {disabled && (
            <div className="flex flex-wrap items-center justify-between gap-4 border-t border-zinc-200 px-4 py-3 dark:border-zinc-800">
              <p className="max-w-sm text-sm text-zinc-600 dark:text-zinc-400">
                Fix the receiver first: enabling resets the failure count, and the next failures count from zero.
              </p>
              <ActionButton action={enableEndpoint.bind(null, ep.id)} label="Enable endpoint" pendingLabel="Enabling…" />
            </div>
          )}
        </Card>

        <Card title="Replay failed deliveries">
          <div className="space-y-3 p-4">
            <p className="text-sm text-zinc-600 dark:text-zinc-400">
              Sends again every delivery to this endpoint that ran out of attempts, each with a fresh retry schedule.
            </p>
            {disabled ? <p className="text-sm text-amber-700 dark:text-amber-400">Enable the endpoint to replay its deliveries.</p> : <ReplayEndpointForm action={replayEndpoint.bind(null, ep.id)} />}
          </div>
        </Card>

        <Card title="Signing secret">
          <div className="space-y-3 p-4">
            <p className="text-sm text-zinc-600 dark:text-zinc-400">
              Rotating gives a new secret. While the old one is still valid, every delivery carries both signatures, so the receiver can switch whenever it likes.
            </p>
            <RotateSecretForm action={rotateSecret.bind(null, ep.id)} />
          </div>
        </Card>
      </div>
    </>
  );
}
