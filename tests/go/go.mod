module tests

go 1.23.4

replace tests/common => ./common

require (
	github.com/google/go-cmp v0.6.0
	github.com/gopacket/gopacket v1.3.1
	github.com/stretchr/testify v1.10.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
