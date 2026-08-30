FROM golang:1.25.13-alpine AS build

WORKDIR /src
COPY pkg ./pkg
COPY services/envoy-authz-adapter ./services/envoy-authz-adapter

ENV GOWORK=off CGO_ENABLED=0
RUN apk add --no-cache git ca-certificates
RUN cd services/envoy-authz-adapter && go build -ldflags "-s -w" -o /out/envoy-authz-adapter .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/envoy-authz-adapter /usr/local/bin/envoy-authz-adapter

USER 65532:65532
EXPOSE 9002

ENTRYPOINT ["/usr/local/bin/envoy-authz-adapter"]
