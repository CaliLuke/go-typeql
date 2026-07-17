package gotype

// unwrapResult flattens nested TypeDB result structures.
// TypeDB fetch results may wrap values as {"value": X, "type": {...}}.
func unwrapResult(result map[string]any) map[string]any {
	flat := make(map[string]any, len(result))
	for key, val := range result {
		flat[key] = unwrapValue(val)
	}
	return flat
}

func unwrapValue(val any) any {
	if val == nil {
		return nil
	}
	m, ok := val.(map[string]any)
	if !ok {
		return val
	}
	if v, ok := m["value"]; ok {
		return v
	}
	return val
}
