package lib

import "fmt"

// GetStringFromAnyMap extracts a string value from either map[string]interface{} or map[interface{}]interface{}.
func GetStringFromAnyMap(m any, key string) (string, bool) {
	switch mm := m.(type) {
	case map[string]any:
		if v, ok := mm[key]; ok {
			return fmt.Sprintf("%v", v), true
		}
	case map[any]any:
		if v, ok := mm[key]; ok {
			return fmt.Sprintf("%v", v), true
		}
	}
	return "", false
}
