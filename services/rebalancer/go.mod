module github.com/Milad-Afdasta/TrueNow/services/rebalancer

go 1.21

require (
	github.com/Milad-Afdasta/TrueNow/proto/rebalancer v0.0.0-00010101000000-000000000000
	github.com/gorilla/mux v1.8.1
	google.golang.org/grpc v1.65.0
	google.golang.org/protobuf v1.34.1
)

require (
	golang.org/x/net v0.29.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/text v0.18.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240528184218-531527333157 // indirect
)

replace github.com/Milad-Afdasta/TrueNow/proto/rebalancer => ../../proto/rebalancer
