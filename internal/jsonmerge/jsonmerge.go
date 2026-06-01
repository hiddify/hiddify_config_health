// Package jsonmerge deep-merges two JSON objects.
// Rules:
//   - Objects: keys from override win; keys only in base are kept.
//   - Arrays:  override array replaces base array entirely.
//   - Scalars: override wins.
//   - null override: removes the key.
package jsonmerge

import "encoding/json"

// Merge merges override on top of base. Both inputs must be JSON objects.
// Returns the merged JSON bytes.
func Merge(base, override []byte) ([]byte, error) {
	var b, o map[string]interface{}
	if err := json.Unmarshal(base, &b); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(override, &o); err != nil {
		return nil, err
	}
	merged := mergeMap(b, o)
	return json.Marshal(merged)
}

func mergeMap(base, override map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		if v == nil {
			delete(out, k) // null = remove
			continue
		}
		baseVal, exists := out[k]
		if exists {
			// Both are objects → recurse.
			baseMap, baseIsMap := baseVal.(map[string]interface{})
			overMap, overIsMap := v.(map[string]interface{})
			if baseIsMap && overIsMap {
				out[k] = mergeMap(baseMap, overMap)
				continue
			}
		}
		out[k] = v
	}
	return out
}
