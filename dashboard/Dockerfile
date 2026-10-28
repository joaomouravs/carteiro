# syntax=docker/dockerfile:1

FROM node:22.23.3-alpine AS deps
WORKDIR /app
# Dependencies in their own layer, so changing code does not reinstall them.
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund

FROM deps AS build
COPY . .
RUN npm run build

# The runtime image holds only Next's standalone server: no npm, no
# devDependencies, no source.
FROM node:22.23.3-alpine
WORKDIR /app
ENV NODE_ENV=production \
    PORT=3001 \
    HOSTNAME=0.0.0.0 \
    NEXT_TELEMETRY_DISABLED=1
COPY --from=build --chown=node:node /app/.next/standalone ./
COPY --from=build --chown=node:node /app/.next/static ./.next/static
USER node
EXPOSE 3001
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD wget -qO /dev/null http://127.0.0.1:3001/login || exit 1
CMD ["node", "server.js"]
