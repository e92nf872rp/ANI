FROM golang:1.25.13-alpine AS build

WORKDIR /src
COPY runtimeadmin ./runtimeadmin
COPY pkg ./pkg
COPY services/model-service ./services/model-service

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go work init ./runtimeadmin ./pkg ./services/model-service && \
    cd services/model-service && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/model-service .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/model-service /usr/local/bin/model-service

USER 65532:65532
EXPOSE 9103 9203

ENTRYPOINT ["/usr/local/bin/model-service"]
