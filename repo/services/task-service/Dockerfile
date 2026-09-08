FROM golang:1.25.13-alpine AS build

WORKDIR /src
COPY runtimeadmin ./runtimeadmin
COPY pkg ./pkg
COPY services/task-service ./services/task-service

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go work init ./runtimeadmin ./pkg ./services/task-service && \
    cd services/task-service && \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/task-service .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/task-service /usr/local/bin/task-service

USER 65532:65532
EXPOSE 9104 9204

ENTRYPOINT ["/usr/local/bin/task-service"]
