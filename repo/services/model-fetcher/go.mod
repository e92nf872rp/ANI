module github.com/kubercloud/ani/services/model-fetcher

go 1.25.0

require (
	github.com/kubercloud/ani/pkg v0.0.0
	google.golang.org/grpc v1.83.1
)

require (
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/kubercloud/ani/pkg => ../../pkg
