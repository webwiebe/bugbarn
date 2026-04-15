FROM node:22-alpine AS web-build

WORKDIR /app/web

COPY web/package*.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM caddy:2.8-alpine

WORKDIR /srv

COPY --from=web-build /app/web/dist /srv/dist
COPY web/index.html /srv/index.html
COPY web/styles.css /srv/styles.css

EXPOSE 8080

CMD ["caddy", "file-server", "--root", "/srv", "--listen", ":8080"]
