# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY sdks/go ./sdks/go

ARG COMMIT=unknown BUILD_TIME=unknown
RUN go build -ldflags "-X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" -o /out/bugbarn ./cmd/bugbarn

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
#
# CPU cap: the build host also runs the CI runners, and an uncapped test stage
# held the buildkit container at 780% CPU and the host at 0-2% idle. GOMAXPROCS
# bounds the compiler, linker and test binaries; -p bounds how many packages
# vet, test and build run at once. Raise both together, never one alone.
FROM deps AS test
ENV GOMAXPROCS=4
COPY . .
RUN go vet -p 4 ./... && go test -p 4 ./... && go build -p 4 ./... && \
    (cd sdks/go && go vet -p 4 ./... && go test -p 4 ./... && go build -p 4 ./...)

FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates
RUN addgroup -S bugbarn && adduser -S bugbarn -G bugbarn

COPY --from=build /out/bugbarn /usr/local/bin/bugbarn
COPY deploy/docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

USER bugbarn

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
