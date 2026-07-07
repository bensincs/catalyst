# Cortex console (Next.js BFF) image.
# Build from the REPO ROOT (keeps one build context for both images):
#   docker build -f web/Dockerfile -t cortex-console:dev .
#   az acr build -r <acr> -t cortex-console:latest -f web/Dockerfile .
#
# Relies on `output: "standalone"` in web/next.config.mjs. Runtime config
# (AUTH_*, CORTEX_API_URL, ...) is injected as container env by control-plane/infra.

FROM node:20-alpine AS deps
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci

FROM node:20-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY web/ ./
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:20-alpine AS run
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=3000
ENV HOSTNAME=0.0.0.0
# Next standalone output: a minimal server.js + traced node_modules, plus the
# static assets it does not inline. (No public/ assets today — add
# `COPY --from=build /app/public ./public` here if you introduce them.)
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
USER node
EXPOSE 3000
CMD ["node", "server.js"]
