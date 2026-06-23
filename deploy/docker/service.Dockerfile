# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY sdks/go ./sdks/go

RUN go build -o /out/bugbarn ./cmd/bugbarn

# --- CI-only stages (not part of the deploy image; the final runtime stage
# below depends only on `build`, so a plain `docker build` skips these). ---
#
# deps: download modules and pre-compile the heavy third-party dependency
# closure into the Go build cache. modernc.org/sqlite (-> modernc/libc, a
# transpiled-from-C SQLite) dominates compile time. This work lands in a layer
# keyed only on go.{mod,sum}, so `--cache-to/--cache-from type=registry` shares
# it across every build runner via GHCR — whichever machine picks up the job
# starts from a warm cache instead of recompiling SQLite from scratch.
FROM golang:1.26-alpine AS deps
WORKDIR /src
COPY go.mod go.sum ./
COPY sdks/go/go.mod sdks/go/go.sum ./sdks/go/
RUN go mod download
RUN go build std && \
    go build \
      modernc.org/sqlite \
      github.com/XSAM/otelsql \
      go.opentelemetry.io/otel/sdk/trace \
      go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp \
      github.com/redis/go-redis/v9 \
      github.com/pressly/goose/v3

# test: vet + test + build for both modules on top of the warm deps cache, so a
# source change only recompiles app packages. The RUN fails the build on any
# test/vet/build failure, which fails CI.
FROM deps AS test
COPY . .
RUN go vet ./... && go test ./... && go build ./... && \
    (cd sdks/go && go vet ./... && go test ./... && go build ./...)

FROM alpine:3.20 AS litestream
ARG LITESTREAM_VERSION=0.3.13
RUN apk add --no-cache ca-certificates wget && \
    wget -qO /tmp/litestream.tar.gz \
      "https://github.com/benbjohnson/litestream/releases/download/v${LITESTREAM_VERSION}/litestream-v${LITESTREAM_VERSION}-linux-amd64.tar.gz" && \
    tar -C /usr/local/bin -xzf /tmp/litestream.tar.gz litestream

FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates
RUN addgroup -S bugbarn && adduser -S bugbarn -G bugbarn

COPY --from=build /out/bugbarn /usr/local/bin/bugbarn
COPY --from=litestream /usr/local/bin/litestream /usr/local/bin/litestream
COPY deploy/docker/litestream.yml /etc/litestream.yml
COPY deploy/docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

USER bugbarn

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
