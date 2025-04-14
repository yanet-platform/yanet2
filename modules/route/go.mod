module github.com/yanet-platform/yanet2/modules/route

go 1.24.1

require (
	github.com/c2h5oh/datasize v0.0.0-20231215233829-aa82cc1e6500
	github.com/stretchr/testify v1.10.0
	github.com/vishvananda/netlink v1.3.0
	github.com/yanet-platform/yanet2/common/go v0.0.0
	github.com/yanet-platform/yanet2/controlplane v0.0.0
	go.uber.org/zap v1.27.0
	golang.org/x/sync v0.11.0
	golang.org/x/sys v0.30.0
	google.golang.org/grpc v1.70.0
	google.golang.org/protobuf v1.35.2
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/vishvananda/netns v0.0.4 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/net v0.36.0 // indirect
	golang.org/x/text v0.22.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241202173237-19429a94021a // indirect
)

replace (
	github.com/yanet-platform/yanet2/common/go => ../../common/go
	github.com/yanet-platform/yanet2/controlplane => ../../controlplane
)
