FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY sdks/go ./sdks/go

RUN go build -o /out/bugbarn ./cmd/bugbarn

FROM alpine:3.20

WORKDIR /app

RUN addgroup -S bugbarn && adduser -S bugbarn -G bugbarn

COPY --from=build /out/bugbarn /usr/local/bin/bugbarn

USER bugbarn

EXPOSE 8080

CMD ["bugbarn"]
