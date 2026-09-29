FROM golang:1.25.13-alpine AS build

ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /src
COPY runtimeadmin ./runtimeadmin
COPY pkg ./pkg
COPY services/reconcile-worker ./services/reconcile-worker

# workspace 模式：pkg 依赖 runtimeadmin（bootstrap → runtimeadmin），单模块
# GOWORK=off 构建会因 go.mod 缺 replace 失败；与 gateway Dockerfile 的
# go work init 模式保持一致。
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go work init ./runtimeadmin ./pkg ./services/reconcile-worker && \
    cd services/reconcile-worker && \
    CGO_ENABLED=0 go build -tags stdjson -ldflags "-s -w" -o /out/reconcile-worker .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/reconcile-worker /usr/local/bin/reconcile-worker

USER 65532:65532
EXPOSE 9205

ENTRYPOINT ["/usr/local/bin/reconcile-worker"]
