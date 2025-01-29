.PHONY: ynpb build modules/route/routepb

GO=/Users/esafronov/.ya/tools/v4/6608868508/bin/go
PROTOC := protoc

ynpb: ynpb/gateway.proto ynpb/logging.proto
	@echo "+ $@"
	$(PROTOC) -I ./ynpb --go_out=./$@ --go_opt=paths=source_relative --go-grpc_out=./$@ --go-grpc_opt=paths=source_relative $^

modules/route/routepb: modules/route/routepb/route.proto
	@echo "+ $@"
	@if ! which protoc > /dev/null; then echo "protoc protobuf compiler required for build"; exit 1; fi;
	@protoc -I ./modules/route/routepb --go_out=./$@ --go_opt=paths=source_relative --go-grpc_out=./$@ --go-grpc_opt=paths=source_relative $<

build: ynpb modules/route/routepb
	$(GO) build -o ../build/yanet-controlplane cmd/yncp-director/main.go
