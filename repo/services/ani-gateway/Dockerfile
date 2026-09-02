FROM golang:1.25.13-alpine AS build

ENV GOPROXY=https://goproxy.cn,direct
ENV GOWORK=off

WORKDIR /src
COPY pkg ./pkg
COPY services/ani-gateway ./services/ani-gateway

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd services/ani-gateway && \
    CGO_ENABLED=0 go build -tags stdjson -ldflags "-s -w" -o /out/ani-gateway .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/ani-gateway /usr/local/bin/ani-gateway
COPY ani-tools/helm /usr/local/bin/helm
COPY ani-tools/vcluster /usr/local/bin/vcluster
COPY ani-tools/kubectl /usr/local/bin/kubectl

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/ani-gateway"]
