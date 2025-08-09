module github.com/Milad-Afdasta/TrueNow/services/query-api

go 1.23.0

require (
	github.com/Milad-Afdasta/TrueNow/shared/proto v0.0.0-00010101000000-000000000000
	github.com/gorilla/mux v1.8.1
	github.com/redis/go-redis/v9 v9.12.0
	github.com/sirupsen/logrus v1.9.3
	google.golang.org/grpc v1.65.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/stretchr/testify v1.9.0 // indirect
	golang.org/x/net v0.29.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/text v0.18.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240528184218-531527333157 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
)

replace github.com/Milad-Afdasta/TrueNow/shared/proto => ../../shared/proto
