package tqlgen

import (
	"regexp"
	"strings"
)

// ExtractAnnotations parses comment annotations of the form "# @key value"
// from schema text. Returns a map of type name -> annotation map.
func ExtractAnnotations(input string) map[string]map[string]string {
	result := make(map[string]map[string]string)

	lines := strings.Split(input, "\n")
	var pendingAnnots []struct{ key, val string }

	// Match: # @key or # @key(value) or # @key value
	annotRe := regexp.MustCompile(`^#\s*@(\w+)(?:\(([^)]*)\)|\s+(.+))?$`)
	typeRe := regexp.MustCompile(`^(entity|relation|attribute|struct)\s+([\w-]+)`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for annotation comment
		if m := annotRe.FindStringSubmatch(trimmed); m != nil {
			val := m[2] // from (value)
			if val == "" {
				val = m[3] // from space-separated
			}
			pendingAnnots = append(pendingAnnots, struct{ key, val string }{m[1], strings.TrimSpace(val)})
			continue
		}

		// Blank lines and plain (non-annotation) comments between the
		// annotations and the definition do not clear pending annotations.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// A code line: attach pending annotations if it defines a type,
		// then clear them either way.
		if len(pendingAnnots) > 0 {
			if m := typeRe.FindStringSubmatch(trimmed); m != nil {
				annots := make(map[string]string)
				for _, a := range pendingAnnots {
					annots[a.key] = a.val
				}
				result[m[2]] = annots
			}
			pendingAnnots = nil
		}
	}

	return result
}
