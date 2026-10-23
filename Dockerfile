FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /store ./cmd/store
RUN curl --fail --location --retry 3 https://github.com/jeethsuresh/appstore/releases/download/v0.3.2/appstore-android.apk -o /appstore.apk \
    && echo '55c5520b399fb5b3624dfc245cf2e9e5f4ec66510789a77db64a9d65c5ba9cbe  /appstore.apk' | sha256sum -c -

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates aapt apksigner default-jre-headless && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home store && mkdir /data && chown store:store /data
COPY --from=build /store /usr/local/bin/store
COPY --from=build /appstore.apk /usr/local/share/store/appstore.apk
COPY config /etc/private-store
USER 10001:10001
ENV LISTEN_ADDR=:8080 DATA_DIR=/data APPS_CONFIG=/etc/private-store/apps.json
EXPOSE 8080
CMD ["store"]
