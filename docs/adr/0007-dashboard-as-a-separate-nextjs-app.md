# ADR 0007: Build the dashboard as a separate Next.js app on the public API

- **Status:** Accepted
- **Date:** 2026-10-28

## Context

Producers need a UI for what the API already does: follow an event down to each HTTP
attempt, replay what failed, enable an endpoint again, rotate its secret. I weighed two
ways to build it:

- **Go templates with htmx**, rendered inside the Carteiro binary. One artifact to deploy,
  no JavaScript build, and the UI could read the database directly.
- **A separate Next.js app** that only talks to Carteiro through its public HTTP API.

## Decision

I went with a separate Next.js app in `dashboard/`, using only the public API. Next.js is
what I use day to day, and I wanted the project to show a front end working with this
back end, not only the back end.

- The API key lives in an encrypted, `httpOnly` session cookie. Every call to Carteiro
  comes from the Next.js server (server components and server actions), so the key never
  reaches browser JavaScript and Carteiro does not need CORS.
- The dashboard has no database and no special access. Anything it shows or does, a
  producer could do with `curl` and the same key.

## Consequences

- Two things to deploy instead of one, but Carteiro itself stays a single binary with no
  UI code in it.
- The dashboard is just another API client, so if the API is missing something, the
  dashboard is missing it too, instead of hiding the gap behind a direct database query.
- It is a separate project with its own toolchain (Node.js and npm) and its own CI job.
