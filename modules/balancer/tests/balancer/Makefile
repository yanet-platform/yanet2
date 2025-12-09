clean:
	go clean -cache
	rm -rf balancer.test

build:
	go test -c -v -asan .

build-debug:
	CGO_CLAGS="-fsanitize=address,undefined -g" CGO_LDFLAGS="-fsanitize=address,undefined -g" go test -v -c -asan .

run:
	./balancer.test -test.v -test.run=$(Test)

debug:
	gdb --args ./balancer.test -test.v -test.run=$(Test)

test-debug:
	CGO_CLAGS="-fsanitize=address,undefined -g" CGO_LDFLAGS="-fsanitize=address,undefined -g" go test -v -run=$(Test) .

test:
	go test -v -run=$(Test) .