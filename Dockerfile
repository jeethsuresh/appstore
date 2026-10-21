FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /store ./cmd/store

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates aapt apksigner default-jre-headless && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home store && mkdir /data && chown store:store /data
COPY --from=build /store /usr/local/bin/store
COPY config /etc/private-store
USER 10001:10001
ENV LISTEN_ADDR=:8080 DATA_DIR=/data APPS_CONFIG=/etc/private-store/apps.json
EXPOSE 8080
CMD ["store"]
