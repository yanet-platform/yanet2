module github.com/yanet-platform/yanet2/controlplane

go 1.22.5

replace github.com/yanet-platform/yanet2/common/go => ../common/go

require (
	github.com/spf13/cobra v1.8.1
	github.com/vishvananda/netlink v1.3.0
	golang.org/x/sys v0.28.0
	google.golang.org/protobuf v1.35.2
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/vishvananda/netns v0.0.4 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/net v0.33.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241202173237-19429a94021a // indirect
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/siderolabs/grpc-proxy v0.5.1
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/testify v1.10.0
	github.com/yanet-platform/yanet2/common/go v0.0.0-00010101000000-000000000000
	go.uber.org/zap v1.27.0
	golang.org/x/sync v0.10.0
	google.golang.org/grpc v1.70.0
	gopkg.in/yaml.v3 v3.0.1
)
