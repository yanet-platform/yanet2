build:
	go clean
	go clean -cache
	go test -c -v -asan -run=Packet .

test: build
	./balancer.test