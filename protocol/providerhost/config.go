package providerhost

import (
	"encoding/json"
	"fmt"
)

// Config decodes selected string values from provider initialization config.
func Config(values map[string]json.RawMessage, into map[string]*string) error {
	for key, target := range into {
		if len(values[key]) == 0 {
			continue
		}
		if err := json.Unmarshal(values[key], target); err != nil {
			return fmt.Errorf("decode provider config %s: %w", key, err)
		}
	}
	return nil
}
