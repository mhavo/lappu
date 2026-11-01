# Build: static Go binary with the viewer (public/) embedded.
FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lappu ./cmd/lappu \
    && mkdir -p /out/var/www/data /out/var/www/quotes \
    && chmod 0700 /out/var/www/data

# Run: no shell, no package manager.
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/lappu /lappu
COPY --from=build --chown=33:33 /out/var/www /var/www
# UID/GID 33 (www-data) matches the data/ and quotes/ directories of the
# previous PHP image, so existing volumes work without chown. Override with
# `user:` in compose if your volumes belong to someone else.
USER 33:33
# Binding :80 as non-root works because Docker sets
# net.ipv4.ip_unprivileged_port_start=0 in containers. Use LAPPU_LISTEN=:8080 otherwise.
EXPOSE 80
ENTRYPOINT ["/lappu"]
