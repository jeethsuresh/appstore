# Build with the directory containing sibling appstore/ and spacetime/ clones:
# docker build -f appstore/integrations/notifications.Dockerfile .
FROM golang:1.25-bookworm
WORKDIR /src/spacetime/backend
COPY spacetime/backend/go.mod spacetime/backend/go.sum ./
RUN go mod download
COPY spacetime/backend ./
RUN go test -c -o /tmp/spacetime.test
WORKDIR /src/appstore/backend
COPY appstore/backend/go.mod appstore/backend/go.sum ./
RUN go mod download
COPY appstore/backend ./
COPY appstore/config ../config
COPY appstore/android/release-certificate.sha256 ../android/release-certificate.sha256
RUN SPACETIME_TEST_BINARY=/tmp/spacetime.test go test -race -v -run '^TestSpacetimeNotificationEndToEnd$' ./internal/store
