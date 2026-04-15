FROM node:22-alpine AS web-build

WORKDIR /app/web

COPY web/package*.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM node:22-alpine AS sdk-build

WORKDIR /app/sdks/typescript

COPY sdks/typescript/package*.json ./
RUN npm ci

COPY sdks/typescript/ ./
RUN npm run build && npm pack

FROM caddy:2.8-alpine

WORKDIR /srv

COPY --from=web-build /app/web/dist /srv/dist
COPY --from=sdk-build /app/sdks/typescript/bugbarn-typescript-0.1.0.tgz /srv/packages/typescript/bugbarn-typescript-0.1.0.tgz
COPY web/index.html /srv/index.html
COPY web/styles.css /srv/styles.css

EXPOSE 8080

CMD ["caddy", "file-server", "--root", "/srv", "--listen", ":8080"]
