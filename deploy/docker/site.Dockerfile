FROM node:22-alpine AS build
WORKDIR /app
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
COPY site/package.json site/pnpm-lock.yaml site/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY site/ ./
RUN pnpm run build

FROM caddy:2.8-alpine
COPY --from=build /app/dist /srv
EXPOSE 8080
CMD ["caddy", "file-server", "--root", "/srv", "--listen", ":8080"]
