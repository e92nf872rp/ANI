FROM golang:1.26.9-alpine@sha256:cdfd4fe2da6b225d8b40c6b7a105736e548e83ff56d5d8f9394446eeb5eb84e0 AS build

WORKDIR /src
COPY runtimeadmin ./runtimeadmin
COPY pkg ./pkg
COPY services/model-fetcher ./services/model-fetcher
ENV CGO_ENABLED=0
RUN go work init ./runtimeadmin ./pkg ./services/model-fetcher && \
    cd services/model-fetcher && \
    go build -ldflags "-s -w" -o /out/model-fetcher .

FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache ca-certificates && adduser -D -H -u 65532 ani
COPY --from=build /out/model-fetcher /usr/local/bin/model-fetcher
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/model-fetcher"]
