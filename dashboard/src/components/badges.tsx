import type { DeliveryStatus, EndpointStatus } from "@/lib/types.ts";

const base = "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset";

const deliveryStyles: Record<DeliveryStatus, { label: string; className: string }> = {
  succeeded: { label: "Delivered", className: "bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-400/20" },
  pending: { label: "Pending", className: "bg-amber-50 text-amber-800 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-400/20" },
  in_flight: { label: "Sending", className: "bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-400/20" },
  dead: { label: "Failed", className: "bg-rose-50 text-rose-700 ring-rose-600/20 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-400/20" },
};

export function DeliveryBadge({ status }: { status: DeliveryStatus }) {
  const s = deliveryStyles[status];
  return (
    <span className={`${base} ${s.className}`}>
      <span aria-hidden className="size-1.5 rounded-full bg-current" />
      {s.label}
    </span>
  );
}

export function EndpointBadge({ status, reason }: { status: EndpointStatus; reason: string | null }) {
  if (status === "active") {
    return <span className={`${base} ${deliveryStyles.succeeded.className}`}>Active</span>;
  }
  const why = reason === "gone" ? "receiver answered 410 Gone" : reason === "failing" ? "kept failing" : reason;
  return (
    <span className={`${base} ${deliveryStyles.dead.className}`} title={why ?? undefined}>
      Disabled{why ? ` · ${why}` : ""}
    </span>
  );
}

// HTTPStatus colors a response code by class: 2xx worked, anything else did not.
export function HTTPStatus({ code }: { code: number | null }) {
  if (code === null) {
    return <span className="font-mono text-xs text-zinc-500">no response</span>;
  }
  const ok = code >= 200 && code <= 299;
  return (
    <span className={`font-mono text-xs font-semibold ${ok ? "text-emerald-600 dark:text-emerald-400" : "text-rose-600 dark:text-rose-400"}`}>
      {code}
    </span>
  );
}
