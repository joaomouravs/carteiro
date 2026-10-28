"use client";

import { useActionState } from "react";

import { buttonStyles } from "@/components/ui.tsx";

import { signIn, type LoginState } from "./actions.ts";

export function LoginForm() {
  const [state, action, pending] = useActionState<LoginState, FormData>(signIn, {});
  return (
    <form action={action} className="space-y-4">
      <div>
        <label htmlFor="api_key" className="block text-sm font-medium">
          API key
        </label>
        <input
          id="api_key"
          name="api_key"
          type="password"
          required
          autoComplete="off"
          spellCheck={false}
          placeholder="crt_…"
          aria-invalid={state.error ? true : undefined}
          aria-describedby={state.error ? "login-error" : undefined}
          className="mt-1.5 block w-full rounded-lg border-0 bg-white px-3 py-2 font-mono text-sm ring-1 ring-inset ring-zinc-300 placeholder:text-zinc-400 focus:ring-2 focus:ring-indigo-600 dark:bg-zinc-900 dark:ring-zinc-700"
        />
      </div>
      {state.error && (
        <p id="login-error" role="alert" className="rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700 dark:bg-rose-500/10 dark:text-rose-300">
          {state.error}
        </p>
      )}
      <button type="submit" disabled={pending} className={`${buttonStyles.primary} w-full py-2`}>
        {pending ? "Checking the key…" : "Sign in"}
      </button>
    </form>
  );
}
