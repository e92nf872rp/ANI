FROM golang:1.26.9-alpine@sha256:cdfd4fe2da6b225d8b40c6b7a105736e548e83ff56d5d8f9394446eeb5eb84e0 AS build

WORKDIR /src
COPY runtimeadmin ./runtimeadmin
COPY pkg ./pkg
COPY services/model-service ./services/model-service

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go work init ./runtimeadmin ./pkg ./services/model-service && \
    cd services/model-service && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/model-service . && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/model-import-worker ./cmd/model-import-worker

FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc AS runtime

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/model-service /usr/local/bin/model-service

USER 65532:65532
EXPOSE 9103 9105 9203

ENTRYPOINT ["/usr/local/bin/model-service"]

FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc AS model-import-worker

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/model-import-worker /usr/local/bin/model-import-worker

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/model-import-worker"]
