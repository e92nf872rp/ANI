module github.com/kubercloud/ani/services/metering-service

go 1.25.0

require (
	github.com/kubercloud/ani/pkg v0.0.0
	github.com/nats-io/nats.go v1.36.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.17.2 // indirect
	github.com/nats-io/nkeys v0.4.7 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.51.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)

replace github.com/kubercloud/ani/pkg => ../../pkg
