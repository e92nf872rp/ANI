FROM golang:1.25.13-alpine AS build

ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /src
COPY runtimeadmin ./runtimeadmin
COPY pkg ./pkg
COPY services/ani-gateway ./services/ani-gateway

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
	go work init ./runtimeadmin ./pkg ./services/ani-gateway && \
    cd services/ani-gateway && \
    CGO_ENABLED=0 go build -tags stdjson -ldflags "-s -w" -o /out/ani-gateway .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget && \
    wget -q https://dl.k8s.io/release/v1.36.0/bin/linux/amd64/kubectl -O /usr/local/bin/kubectl && \
    chmod +x /usr/local/bin/kubectl && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/ani-gateway /usr/local/bin/ani-gateway

USER 65532:65532
EXPOSE 8080 9200

ENTRYPOINT ["/usr/local/bin/ani-gateway"]
