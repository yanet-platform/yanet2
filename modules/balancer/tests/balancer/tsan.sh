export CC=clang
export CXX=clang++
export CGO_CFLAGS="-fsanitize=thread -fno-omit-frame-pointer"
export CGO_CXXFLAGS="-fsanitize=thread -fno-omit-frame-pointer"
export CGO_LDFLAGS="-fsanitize=thread"

go test ./... -count=1 -ldflags='-linkmode=external -extld=clang'