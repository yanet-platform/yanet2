clean:
	go clean -cache
	rm -rf balancer.test

compile:
	go test -c -v -asan .

asan:
	CGO_CLAGS="-fsanitize=address,undefined -g" CGO_LDFLAGS="-fsanitize=address,undefined -g" go test -v -c -asan .

run:
	./balancer.test -test.v -test.run=$(Test)

debug:
	gdb --args ./balancer.test -test.v -test.run=$(Test)
