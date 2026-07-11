FROM node:22-alpine AS web-build

WORKDIR /app/web

COPY web/package*.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM node:22-alpine AS site-build

WORKDIR /app/site

COPY site/package*.json ./
RUN npm ci

COPY site/ ./
RUN npm run build

FROM node:22-alpine AS sdk-build

WORKDIR /app/sdks/typescript

COPY sdks/typescript/package*.json ./
RUN npm ci

COPY sdks/typescript/ ./
# Derive a stable 12-char content hash from source files. The same SDK source
# always produces the same hash, so the tarball URL is immutable per content.
# When the SDK source changes the hash changes, producing a new URL.
RUN SDK_HASH=$(find src -type f | sort | xargs sha256sum | sha256sum | cut -c1-12) && \
    npm version "0.1.0-${SDK_HASH}" --no-git-tag-version && \
    npm run build && npm pack

FROM caddy:2.8-alpine

WORKDIR /srv

# Dashboard SPA under /app/. build.mjs emits content-hashed, immutable assets
# (app-<hash>.js + styles-<hash>.css) plus a templated index.html/sw.js that
# reference them by their hashed names. The Caddyfile serves the hashed assets
# immutable and the shell no-cache, so a new deploy is picked up immediately with
# no CDN purge and no stale bare-module URLs.
COPY --from=web-build /app/web/dist/app-*.js* /srv/app/dist/
COPY --from=web-build /app/web/dist/styles-*.css /srv/app/dist/
COPY --from=web-build /app/web/dist/index.html /srv/app/index.html
COPY --from=web-build /app/web/dist/sw.js /srv/app/sw.js
COPY web/manifest.json /srv/app/manifest.json
COPY web/icons/ /srv/app/icons/

# Marketing site at root
COPY --from=site-build /app/site/dist /srv/site

# Stage the SDK tarball under /tmp so the entrypoint can copy it to the
# persistent /srv/packages volume on startup, preserving previous versions.
COPY --from=sdk-build /app/sdks/typescript/bugbarn-typescript-*.tgz /tmp/sdk-package/

COPY deploy/docker/Caddyfile /etc/caddy/Caddyfile
COPY deploy/docker/web-entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
