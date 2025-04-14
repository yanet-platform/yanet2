module github.com/yanet-platform/yanet2/controlplane

go 1.24.1

replace github.com/yanet-platform/yanet2/common/go => ../common/go

replace github.com/yanet-platform/yanet2/modules/route => ../modules/route

require (
	github.com/c2h5oh/datasize v0.0.0-20231215233829-aa82cc1e6500
	github.com/spf13/cobra v1.8.1
	github.com/yanet-platform/yanet2/modules/route v0.0.0-00010101000000-000000000000
)

require (
	github.com/vishvananda/netlink v1.3.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/vishvananda/netns v0.0.4 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/net v0.36.0 // indirect
	golang.org/x/text v0.22.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241202173237-19429a94021a // indirect
	google.golang.org/protobuf v1.35.2
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/siderolabs/grpc-proxy v0.5.1
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/testify v1.10.0
	github.com/yanet-platform/yanet2/common/go v0.0.0
	go.uber.org/zap v1.27.0
	golang.org/x/sync v0.11.0
	google.golang.org/grpc v1.70.0
	gopkg.in/yaml.v3 v3.0.1
)
