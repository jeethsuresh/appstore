FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /store ./cmd/store
RUN curl --fail --location --retry 3 https://github.com/jeethsuresh/appstore/releases/download/v0.6.14/appstore-android.apk -o /appstore.apk \
    && echo 'db817ff21664a5cef0b1bd6b057fd054bd5cbee2463b3e91caa47b302e34559f  /appstore.apk' | sha256sum -c -

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates aapt apksigner default-jre-headless && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home store && mkdir /data && chown store:store /data
COPY --from=build /store /usr/local/bin/store
COPY --from=build /appstore.apk /usr/local/share/store/appstore.apk
COPY config /etc/private-store
COPY protocols /etc/private-store/protocols
USER 10001:10001
ENV PROTOCOL_SPEC_FILE=/etc/private-store/protocols/appstore/1.0.0.json LISTEN_ADDR=:8080 DATA_DIR=/data APPS_CONFIG=/etc/private-store/apps.json
EXPOSE 8080
CMD ["store"]
