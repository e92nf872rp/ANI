FROM golang:1.25.13-alpine AS build

WORKDIR /src
COPY pkg ./pkg
COPY sdks/core/go ./sdks/core/go
COPY services/inference-service ./services/inference-service

ENV GOWORK=off CGO_ENABLED=0
RUN apk add --no-cache git ca-certificates
RUN cd services/inference-service && go build -ldflags "-s -w" -o /out/inference-service .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 65532 ani

COPY --from=build /out/inference-service /usr/local/bin/inference-service

USER 65532:65532
EXPOSE 9104 9204

ENTRYPOINT ["/usr/local/bin/inference-service"]
