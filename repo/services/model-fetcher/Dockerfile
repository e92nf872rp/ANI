FROM golang:1.25.13-alpine@sha256:1e0126852075c9c60731c8ba49088448b91f63e2aed97ca9d1a9791622a05946 AS build

WORKDIR /src
COPY pkg ./pkg
COPY services/model-fetcher ./services/model-fetcher
ENV GOWORK=off CGO_ENABLED=0
RUN cd services/model-fetcher && go build -ldflags "-s -w" -o /out/model-fetcher .

FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache ca-certificates && adduser -D -H -u 65532 ani
COPY --from=build /out/model-fetcher /usr/local/bin/model-fetcher
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/model-fetcher"]
