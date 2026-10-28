import { NextResponse, type NextRequest } from "next/server";

import { deleteSession } from "@/lib/session.ts";

// GET is used when a page finds the key revoked: it cannot change cookies
// while rendering, so it redirects here. Signing out on purpose is a POST
// from the sign-out button.
async function signOut(req: NextRequest) {
  await deleteSession();
  const login = new URL("/login", req.url);
  if (req.nextUrl.searchParams.get("reason") === "revoked") login.searchParams.set("reason", "revoked");
  // 303 so the browser follows a POST with a GET.
  return NextResponse.redirect(login, 303);
}

export const GET = signOut;
export const POST = signOut;
