"use server";

// Mutations, run on the server with the session's key. Each one checks the
// session again: a server action is a public POST endpoint, and hiding its
// button from signed-out visitors protects nothing.
import { revalidatePath } from "next/cache";

import { ApiError } from "@/lib/api.ts";
import { requireSession } from "@/lib/session.ts";

export type ActionState = { ok?: boolean; message?: string; secret?: string };

function failure(err: unknown): ActionState {
  if (err instanceof ApiError && err.status !== 500) return { ok: false, message: err.message };
  console.error("dashboard action", err);
  return { ok: false, message: "Something went wrong talking to Carteiro. Try again." };
}

export async function replayDelivery(deliveryID: string): Promise<ActionState> {
  const { api } = await requireSession();
  try {
    await api.replayDelivery(deliveryID);
  } catch (err) {
    return failure(err);
  }
  revalidatePath(`/deliveries/${deliveryID}`);
  return { ok: true, message: "Queued again. It will be sent in a moment." };
}

export async function enableEndpoint(endpointID: string): Promise<ActionState> {
  const { api } = await requireSession();
  try {
    await api.enableEndpoint(endpointID);
  } catch (err) {
    return failure(err);
  }
  revalidatePath(`/endpoints/${endpointID}`);
  revalidatePath("/endpoints");
  return { ok: true, message: "Enabled. Deliveries that died while it was off can be replayed below." };
}

const GRACE_PERIODS: Record<string, number> = { "24h": 86_400, "1h": 3_600, now: 0 };

export async function rotateSecret(endpointID: string, _prev: ActionState, form: FormData): Promise<ActionState> {
  const grace = GRACE_PERIODS[String(form.get("grace"))];
  if (grace === undefined) return { ok: false, message: "Choose how long the old secret stays valid." };
  const { api } = await requireSession();
  try {
    const ep = await api.rotateSecret(endpointID, grace);
    // The secret goes back to this one response and nowhere else: it is
    // not stored, and it is gone once the page is left.
    return { ok: true, secret: ep.secret };
  } catch (err) {
    return failure(err);
  }
}

const PERIODS: Record<string, number> = { "1h": 3_600_000, "24h": 86_400_000, "7d": 7 * 86_400_000 };

export async function replayEndpoint(endpointID: string, _prev: ActionState, form: FormData): Promise<ActionState> {
  const period = PERIODS[String(form.get("period"))];
  if (period === undefined) return { ok: false, message: "Choose a period." };
  const { api } = await requireSession();
  try {
    // The server computes the start, so the user's time zone cannot shift it.
    const { replayed } = await api.replayEndpoint(endpointID, new Date(Date.now() - period).toISOString());
    revalidatePath(`/endpoints/${endpointID}`);
    return {
      ok: true,
      message: replayed === 0 ? "No failed deliveries in that period." : `${replayed} failed ${replayed === 1 ? "delivery" : "deliveries"} queued again.`,
    };
  } catch (err) {
    return failure(err);
  }
}
