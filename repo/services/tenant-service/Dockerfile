FROM golang:1.25.13-alpine AS build

WORKDIR /src
COPY runtimeadmin ./runtimeadmin
COPY pkg ./pkg
COPY sdks/core/go ./sdks/core/go
COPY services/pkg ./services/pkg
COPY services/tenant-service ./services/tenant-service

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go work init ./runtimeadmin ./pkg ./sdks/core/go ./services/pkg ./services/tenant-service && \
    cd services/tenant-service && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/tenant-service .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/tenant-service /usr/local/bin/tenant-service

USER 65532:65532
EXPOSE 9105 9205

ENTRYPOINT ["/usr/local/bin/tenant-service"]
