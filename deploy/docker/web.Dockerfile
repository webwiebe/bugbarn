FROM alpine:3.20

WORKDIR /app

RUN addgroup -S bugbarn && adduser -S bugbarn -G bugbarn

USER bugbarn

CMD ["sh", "-c", "echo 'BugBarn web placeholder'; sleep infinity"]

