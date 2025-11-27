package balancer

import (
	"fmt"
	"testing"

	mock "github.com/yanet-platform/yanet2/mock/go"
)

var Mock *mock.YanetMock

func TestMain(m *testing.M) {
	config := mock.YanetMockConfig{
		CpMemory: 1 << 29,
		DpMemory: 1 << 25,
		Workers:  1,
		Devices: []mock.YanetMockDeviceConfig{
			{
				Id:   0,
				Name: "01:00.0",
			},
		},
	}
	mock, err := mock.NewYanetMock(&config)
	if err != nil {
		msg := fmt.Sprintf("failed to create yanet mock: %w", err)
		panic(msg)
	}
	Mock = mock
}
