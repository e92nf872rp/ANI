FROM dockerproxy.net/library/golang:1.23-alpine AS build

WORKDIR /src
COPY pkg ./pkg
COPY services/ani-gateway ./services/ani-gateway

ENV GOWORK=off
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd services/ani-gateway && \
    CGO_ENABLED=0 go build -tags stdjson -ldflags "-s -w" -o /out/ani-gateway .

FROM dockerproxy.net/library/alpine:3.20 AS tools

ARG HELM_VERSION=v3.19.0
ARG VCLUSTER_VERSION=v0.34.1

RUN apk add --no-cache ca-certificates curl tar && \
    curl -fsSL "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" -o /tmp/helm.tar.gz && \
    tar -xzf /tmp/helm.tar.gz -C /tmp && \
    install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm && \
    curl -fsSL "https://github.com/loft-sh/vcluster/releases/download/${VCLUSTER_VERSION}/vcluster-linux-amd64" -o /usr/local/bin/vcluster && \
    chmod 0755 /usr/local/bin/vcluster

FROM dockerproxy.net/library/alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/ani-gateway /usr/local/bin/ani-gateway
COPY --from=tools /usr/local/bin/helm /usr/local/bin/helm
COPY --from=tools /usr/local/bin/vcluster /usr/local/bin/vcluster

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/ani-gateway"]
