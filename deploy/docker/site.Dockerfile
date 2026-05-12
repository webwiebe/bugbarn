FROM node:22-alpine AS build
WORKDIR /app
COPY site/package*.json ./
RUN npm ci
COPY site/ ./
RUN npm run build

FROM caddy:2.8-alpine
COPY --from=build /app/dist /srv
EXPOSE 8080
CMD ["caddy", "file-server", "--root", "/srv", "--listen", ":8080"]
