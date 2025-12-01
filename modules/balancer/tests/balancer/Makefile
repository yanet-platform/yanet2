clear:
	go clean -cache
	rm -rf balancer.test

compile:
	go test -c -v -asan .

run: compile
	./balancer.test -test.v
