FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY sdks/go ./sdks/go

RUN go build -o /out/bugbarn ./cmd/bugbarn

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
