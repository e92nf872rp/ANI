FROM golang:1.25.13-alpine AS build

ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /src
COPY runtimeadmin ./runtimeadmin
COPY pkg ./pkg
COPY sdks/core/go ./sdks/core/go
COPY services/pkg ./services/pkg
COPY services/platform-settings-service ./services/platform-settings-service

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go work init ./runtimeadmin ./pkg ./sdks/core/go ./services/pkg ./services/platform-settings-service && \
    cd services/platform-settings-service && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/platform-settings-service .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/platform-settings-service /usr/local/bin/platform-settings-service

USER 65532:65532
EXPOSE 9106 9206

ENTRYPOINT ["/usr/local/bin/platform-settings-service"]
