"use client";

import { useActionState, useState } from "react";

import type { ActionState } from "@/app/(app)/actions.ts";

import { buttonStyles } from "./ui.tsx";

function Feedback({ state }: { state: ActionState }) {
  if (!state.message) return null;
  return (
    <p role="status" className={`text-sm ${state.ok ? "text-emerald-700 dark:text-emerald-400" : "text-rose-700 dark:text-rose-400"}`}>
      {state.message}
    </p>
  );
}

// ActionButton runs one server action and shows how it went.
export function ActionButton({
  action,
  label,
  pendingLabel,
  disabled,
  disabledReason,
  variant = "primary",
}: {
  action: () => Promise<ActionState>;
  label: string;
  pendingLabel: string;
  disabled?: boolean;
  disabledReason?: string;
  variant?: keyof typeof buttonStyles;
}) {
  const [state, run, pending] = useActionState<ActionState>(() => action(), {});
  return (
    <form action={run} className="flex flex-col items-end gap-2">
      <button type="submit" disabled={disabled || pending} title={disabled ? disabledReason : undefined} className={buttonStyles[variant]}>
        {pending ? pendingLabel : label}
      </button>
      <Feedback state={state} />
    </form>
  );
}

const select =
  "rounded-lg border-0 bg-white py-1.5 pr-8 pl-3 text-sm ring-1 ring-inset ring-zinc-300 focus:ring-2 focus:ring-indigo-600 dark:bg-zinc-900 dark:ring-zinc-700";

export function ReplayEndpointForm({ action }: { action: (prev: ActionState, form: FormData) => Promise<ActionState> }) {
  const [state, run, pending] = useActionState(action, {});
  return (
    <form action={run} className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor="period" className="text-sm">
          Failed deliveries from the last
        </label>
        <select id="period" name="period" defaultValue="24h" className={select}>
          <option value="1h">hour</option>
          <option value="24h">24 hours</option>
          <option value="7d">7 days</option>
        </select>
        <button type="submit" disabled={pending} className={buttonStyles.secondary}>
          {pending ? "Queuing…" : "Replay"}
        </button>
      </div>
      <Feedback state={state} />
    </form>
  );
}

export function RotateSecretForm({ action }: { action: (prev: ActionState, form: FormData) => Promise<ActionState> }) {
  const [state, run, pending] = useActionState(action, {});
  const [copied, setCopied] = useState(false);
  return (
    <div className="space-y-3">
      <form action={run} className="flex flex-wrap items-center gap-2">
        <label htmlFor="grace" className="text-sm">
          Keep the old secret valid for
        </label>
        <select id="grace" name="grace" defaultValue="24h" className={select}>
          <option value="24h">24 hours</option>
          <option value="1h">1 hour</option>
          <option value="now">no time: it leaked</option>
        </select>
        <button type="submit" disabled={pending} className={buttonStyles.secondary}>
          {pending ? "Rotating…" : "Rotate secret"}
        </button>
      </form>
      {state.secret ? (
        <div role="status" className="space-y-2 rounded-lg bg-amber-50 p-3 text-sm dark:bg-amber-500/10">
          <p className="font-medium text-amber-900 dark:text-amber-200">
            Copy the new secret now: it is shown only this once. Until the grace period ends, deliveries are signed with both.
          </p>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded bg-white px-2 py-1 font-mono text-xs dark:bg-zinc-900">{state.secret}</code>
            <button
              type="button"
              className={buttonStyles.secondary}
              onClick={async () => {
                await navigator.clipboard.writeText(state.secret ?? "");
                setCopied(true);
              }}
            >
              {copied ? "Copied" : "Copy"}
            </button>
          </div>
        </div>
      ) : (
        <Feedback state={state} />
      )}
    </div>
  );
}
