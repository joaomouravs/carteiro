"use server";

import { redirect } from "next/navigation";

import { ApiError, createClient } from "@/lib/api.ts";
import { apiURL, writeSession } from "@/lib/session.ts";

export type LoginState = { error?: string };

// signIn checks the key against Carteiro before keeping it: a typo or a
// revoked key is reported here, not on the first page that uses it.
export async function signIn(_prev: LoginState, form: FormData): Promise<LoginState> {
  const apiKey = String(form.get("api_key") ?? "").trim();
  if (!apiKey.startsWith("crt_")) {
    return { error: "An API key starts with crt_. Your operator issues it for your application." };
  }
  let app;
  try {
    app = await createClient(apiURL(), apiKey).me();
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      return { error: "Carteiro does not accept this key. It may have been revoked." };
    }
    console.error("sign in", err);
    return { error: "Carteiro could not be reached. Try again in a moment." };
  }
  await writeSession({ apiKey, appID: app.id, appName: app.name });
  redirect("/events");
}
