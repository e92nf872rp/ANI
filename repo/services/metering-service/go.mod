module github.com/kubercloud/ani/services/metering-service

go 1.25.0

require (
	github.com/kubercloud/ani/pkg v0.0.0
	github.com/nats-io/nats.go v1.36.0
)

replace github.com/kubercloud/ani/pkg => ../../pkg
