FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /store ./cmd/store
RUN curl --fail --location --retry 3 https://github.com/jeethsuresh/appstore/releases/download/v0.3.0/appstore-android.apk -o /appstore.apk \
    && echo 'e48082f87f07ab0e07de547e3ce22db1bb92ac5de9b46f5fc5f565d68d02c0d9  /appstore.apk' | sha256sum -c -

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates aapt apksigner default-jre-headless && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home store && mkdir /data && chown store:store /data
COPY --from=build /store /usr/local/bin/store
COPY --from=build /appstore.apk /usr/local/share/store/appstore.apk
COPY config /etc/private-store
USER 10001:10001
ENV LISTEN_ADDR=:8080 DATA_DIR=/data APPS_CONFIG=/etc/private-store/apps.json
EXPOSE 8080
CMD ["store"]
