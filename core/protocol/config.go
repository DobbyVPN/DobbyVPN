package protocol

// Config is the accepted protocol payload. Parsing happens once at acceptance;
// attempt-specific engines clone fields before adding local credentials, DNS
// addresses or routing options. It never crosses a native frontend boundary.
type Config struct {
	OutlineURL  string
	Xray        map[string]any
	TrustTunnel map[string]any
}

func (c Config) Empty() bool {
	return c.OutlineURL == "" && len(c.Xray) == 0 && len(c.TrustTunnel) == 0
}

// CloneFields also normalizes TOML array tables to the same []any shape as JSON
// arrays, so Xray's engine need not serialize and parse accepted data again.
func CloneFields(fields map[string]any) map[string]any {
	cloned := make(map[string]any, len(fields))
	for key, value := range fields {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return CloneFields(value)
	case []any:
		cloned := make([]any, len(value))
		for i, item := range value {
			cloned[i] = cloneValue(item)
		}
		return cloned
	case []map[string]any:
		cloned := make([]any, len(value))
		for i, item := range value {
			cloned[i] = CloneFields(item)
		}
		return cloned
	case []string:
		cloned := make([]any, len(value))
		for i, item := range value {
			cloned[i] = item
		}
		return cloned
	default:
		return value
	}
}
