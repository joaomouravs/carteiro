# ADR 0007: Build the dashboard as a separate Next.js app on the public API

- **Status:** Accepted
- **Date:** 2026-10-28

## Context

Producers need a UI to do what the API already allows: follow an event down to each HTTP
attempt, replay what failed, enable an endpoint again, and rotate its secret. Two shapes
were considered:

- **Server-rendered Go templates with htmx**, inside the Carteiro binary. One artifact to
  deploy, no JavaScript build, and the UI could read the database directly.
- **A separate Next.js app** that talks to Carteiro only through its public HTTP API.

## Decision

A separate Next.js app in `dashboard/`, using only the public API, chosen by the project
owner. Next.js is the stack the owner works in, and the project should show the
integration between a front end and this back end, not only the back end.

- The API key lives in an encrypted, `httpOnly` session cookie. Every call to Carteiro is
  made by the Next.js server (server components, server actions), so the key never reaches
  browser JavaScript, and Carteiro needs no CORS.
- The dashboard has no database and no privileged access: anything it shows or does, a
  producer could do with `curl` and the same key.

## Consequences

- Two deployables instead of one; Carteiro itself stays a single binary with no UI code.
- The dashboard is a client of the API like any other, so a gap in the API shows up as a
  gap in the dashboard instead of being papered over with a direct database query.
- It is an independent project with its own toolchain (Node.js, npm) and its own CI job.
