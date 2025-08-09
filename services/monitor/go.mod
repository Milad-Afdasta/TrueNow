module github.com/Milad-Afdasta/TrueNow/services/monitor

go 1.21

require (
	github.com/gdamore/tcell/v2 v2.6.0
	github.com/rivo/tview v0.0.0-20230826224341-9754ab44dc1c
)

require (
	github.com/gdamore/encoding v1.0.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-runewidth v0.0.14 // indirect
	github.com/rivo/uniseg v0.4.3 // indirect
	golang.org/x/net v0.25.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/term v0.24.0 // indirect
	golang.org/x/text v0.18.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240528184218-531527333157 // indirect
	google.golang.org/grpc v1.65.0 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
)

replace (
	github.com/Milad-Afdasta/TrueNow/proto/rebalancer => ../../proto/rebalancer
	github.com/Milad-Afdasta/TrueNow/proto/watermark => ../../proto/watermark
	github.com/Milad-Afdasta/TrueNow/services/autoscaler => ../autoscaler
)
