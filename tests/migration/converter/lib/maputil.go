package lib

import "fmt"

// GetStringFromAnyMap extracts a string value from either map[string]interface{} or map[interface{}]interface{}.
func GetStringFromAnyMap(m interface{}, key string) (string, bool) {
	switch mm := m.(type) {
	case map[string]interface{}:
		if v, ok := mm[key]; ok {
			return fmt.Sprintf("%v", v), true
		}
	case map[interface{}]interface{}:
		if v, ok := mm[key]; ok {
			return fmt.Sprintf("%v", v), true
		}
	}
	return "", false
}
