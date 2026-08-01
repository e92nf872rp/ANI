FROM golang:1.25.12-alpine AS build

WORKDIR /src
COPY pkg ./pkg
COPY services/reconcile-worker ./services/reconcile-worker

ENV GOWORK=off
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd services/reconcile-worker && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/reconcile-worker .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/reconcile-worker /usr/local/bin/reconcile-worker

USER 65532:65532
EXPOSE 9205

ENTRYPOINT ["/usr/local/bin/reconcile-worker"]
