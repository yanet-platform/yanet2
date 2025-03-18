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

	name := fullMethod[1:]
	pos := strings.LastIndex(name, "/")
	if pos < 0 {
		return "", "", fmt.Errorf("method name must be in format `/package.service/method`")
	}

	service, method := name[:pos], name[pos+1:]
	return service, method, nil
}
