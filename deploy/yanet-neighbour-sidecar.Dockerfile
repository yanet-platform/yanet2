# syntax=docker/dockerfile:1

FROM golang:1.24.13-alpine AS build

RUN apk add --no-cache make protobuf protobuf-dev
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 \
    && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN make proto-go \
    && CGO_ENABLED=0 go build -trimpath -o /yanet-neighbour-sidecar \
        ./operators/neighbour-sidecar/cmd/yanet-neighbour-sidecar

FROM alpine:3.23

RUN apk add --no-cache ca-certificates

COPY --from=build /yanet-neighbour-sidecar /usr/local/bin/yanet-neighbour-sidecar
COPY operators/neighbour-sidecar/etc/yanet/yanet-neighbour-sidecar-default.yaml /etc/yanet2/yanet-neighbour-sidecar-default.yaml

EXPOSE 9903

ENTRYPOINT ["/usr/local/bin/yanet-neighbour-sidecar"]
CMD ["-c", "/etc/yanet2/yanet-neighbour-sidecar.yaml"]
