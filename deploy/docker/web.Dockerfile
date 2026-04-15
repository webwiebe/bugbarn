FROM caddy:2.8-alpine

WORKDIR /srv

COPY web/ /srv/

EXPOSE 8080

CMD ["caddy", "file-server", "--root", "/srv", "--listen", ":8080"]
