package xgrpc

import (
	"fmt"
	"strings"
)

// ParseFullMethod parses a full method name into its service, and method
// components.
//
// For example, the full method name `/routepb.RouteService/InsertRoute` will
// be parsed into `routepb.RouteService` and `InsertRoute`.
func ParseFullMethod(fullMethod string) (string, string, error) {
	if !strings.HasPrefix(fullMethod, "/") {
		return "", "", fmt.Errorf("method name must be in format `/package.service/method`")
	}

	service, method, ok := strings.CutLast(fullMethod[1:], "/")
	if !ok {
		return "", "", fmt.Errorf("method name must be in format `/package.service/method`")
	}

	return service, method, nil
}
