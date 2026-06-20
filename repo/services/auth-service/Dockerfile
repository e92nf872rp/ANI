FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.work go.work.sum ./
COPY pkg ./pkg
COPY services/auth-service ./services/auth-service

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd services/auth-service && \
    CGO_ENABLED=0 go build -tags stdjson -ldflags "-s -w" -o /out/auth-service .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/auth-service /usr/local/bin/auth-service

USER 65532:65532
EXPOSE 9101 9201

ENTRYPOINT ["/usr/local/bin/auth-service"]
