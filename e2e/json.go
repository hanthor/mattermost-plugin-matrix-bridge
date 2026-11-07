package e2e

import "encoding/json"

// jsonUnmarshal is a small indirection so test files do not each import
// encoding/json for a single call.
func jsonUnmarshal(raw string, v any) error {
	return json.Unmarshal([]byte(raw), v)
}
