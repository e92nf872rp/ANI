module github.com/kubercloud/ani/services/platform-settings-service

go 1.25.0

require (
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.9.2
	github.com/kubercloud/ani-sdks/core-go v0.0.0
	github.com/kubercloud/ani/pkg v0.0.0
	github.com/kubercloud/ani/services/pkg v0.0.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
)

replace github.com/kubercloud/ani/services/pkg => ../pkg

replace github.com/kubercloud/ani-sdks/core-go => ../../sdks/core/go

replace github.com/kubercloud/ani/pkg => ../../pkg
