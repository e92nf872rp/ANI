module github.com/kubercloud/ani/services/tenant-service

go 1.25.0

require (
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/kubercloud/ani-sdks/core-go v0.0.0
	github.com/kubercloud/ani/pkg v0.0.0
	github.com/kubercloud/ani/services/pkg v0.0.0
	golang.org/x/crypto v0.55.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
)

replace github.com/kubercloud/ani/services/pkg => ../pkg

replace github.com/kubercloud/ani-sdks/core-go => ../../sdks/core/go

replace github.com/kubercloud/ani/pkg => ../../pkg
