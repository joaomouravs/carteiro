// The signed-in producer: their API key and application, in an encrypted,
// httpOnly cookie (docs/adr/0007).
import "server-only";

import { cookies } from "next/headers";
import { notFound, redirect } from "next/navigation";

import { ApiError, createClient, type Client } from "./api.ts";
import { deriveKey, seal, unseal } from "./seal.ts";

const COOKIE = "carteiro_session";
// A working day. The expiry is also inside the sealed value, so a cookie kept
// past its Max-Age (copied out of the browser, say) still stops working.
const MAX_AGE_SECONDS = 8 * 60 * 60;

export type Session = {
  apiKey: string;
  appID: string;
  appName: string;
  expiresAt: number;
};

let key: Buffer | undefined;
function sessionKey(): Buffer {
  key ??= deriveKey(process.env.SESSION_SECRET ?? "");
  return key;
}

export function apiURL(): string {
  const url = process.env.CARTEIRO_API_URL;
  if (!url) throw new Error("CARTEIRO_API_URL is required");
  return url;
}

export async function readSession(): Promise<Session | null> {
  const sealed = (await cookies()).get(COOKIE)?.value;
  if (!sealed) return null;
  const json = unseal(sealed, sessionKey());
  if (!json) return null;
  try {
    const s = JSON.parse(json) as Session;
    return typeof s.apiKey === "string" && s.expiresAt > Date.now() ? s : null;
  } catch {
    return null;
  }
}

export async function writeSession(s: Omit<Session, "expiresAt">): Promise<void> {
  const value: Session = { ...s, expiresAt: Date.now() + MAX_AGE_SECONDS * 1000 };
  (await cookies()).set(COOKIE, seal(JSON.stringify(value), sessionKey()), {
    // Out of reach of page JavaScript, so an XSS bug cannot read the key.
    httpOnly: true,
    // Browsers treat http://localhost as secure, so this works locally too.
    secure: process.env.NODE_ENV === "production",
    // Sent on top-level navigations from other sites, not on their
    // background requests. Server actions also check the Origin header.
    sameSite: "lax",
    path: "/",
    maxAge: MAX_AGE_SECONDS,
  });
}

export async function deleteSession(): Promise<void> {
  (await cookies()).delete(COOKIE);
}

// requireSession is for pages and actions behind the login: it returns the
// session and a client for its key, or sends the visitor to sign in.
export async function requireSession(): Promise<{ session: Session; api: Client }> {
  const session = await readSession();
  if (!session) redirect("/login");
  return { session, api: createClient(apiURL(), session.apiKey) };
}

// orFail turns API errors into what a page should do: a 404 becomes Next's
// not-found page, and a 401 means the key was revoked while signed in, so the
// session ends. Anything else is a real error.
export function orFail(err: unknown): never {
  if (err instanceof ApiError && err.status === 404) notFound();
  if (err instanceof ApiError && err.status === 401) redirect("/logout?reason=revoked");
  throw err;
}
