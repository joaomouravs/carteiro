import type { Metadata } from "next";
import { redirect } from "next/navigation";

import { readSession } from "@/lib/session.ts";

import { LoginForm } from "./login-form.tsx";

export const metadata: Metadata = { title: "Sign in" };

export default async function LoginPage({ searchParams }: { searchParams: Promise<{ reason?: string }> }) {
  if (await readSession()) redirect("/events");
  const { reason } = await searchParams;
  return (
    <main className="flex min-h-screen items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <p className="text-2xl font-semibold tracking-tight">Carteiro</p>
          <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">Every webhook you sent, and what became of it.</p>
        </div>
        <div className="rounded-xl border border-zinc-200 bg-white p-6 shadow-xs dark:border-zinc-800 dark:bg-zinc-900">
          {reason === "revoked" && (
            <p role="status" className="mb-4 rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-500/10 dark:text-amber-300">
              Your API key stopped working, so you were signed out. It may have been revoked.
            </p>
          )}
          <LoginForm />
        </div>
        <p className="mt-6 text-center text-xs text-zinc-500">
          The key stays on this dashboard&apos;s server, in an encrypted cookie your browser cannot read.
        </p>
      </div>
    </main>
  );
}
