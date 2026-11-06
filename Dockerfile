# Compiles on the build machine's own architecture and cross-compiles to the
# target, so a Mac can produce linux/amd64 images without emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
RUN go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
# Same version and driver tags as the Makefile. A cross-compiled `go install`
# lands in a per-platform folder, so the binary is located rather than assumed.
RUN go install \
      -tags 'no_mysql no_sqlite3 no_mssql no_azuresql no_clickhouse no_libsql no_vertica no_ydb' \
      github.com/pressly/goose/v3/cmd/goose@v3.29.0 \
    && install -D "$(find /go/bin -type f -name goose)" /out/goose

# One-shot migration runner. Kept apart from the API image because the process
# never migrates at startup.
FROM alpine:3.22 AS migrate
RUN adduser -D -u 10001 javis
COPY --from=build /out/goose /usr/local/bin/goose
COPY migrations /migrations
USER javis
ENV GOOSE_DRIVER=postgres GOOSE_MIGRATION_DIR=/migrations
ENTRYPOINT ["goose"]

# The default target. Alpine rather than scratch so the container health check
# has `wget`.
FROM alpine:3.22 AS api
RUN adduser -D -u 10001 javis
COPY --from=build /out/api /usr/local/bin/api
USER javis
ENV HTTP_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["api"]
