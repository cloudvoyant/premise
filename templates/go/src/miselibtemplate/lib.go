// Package miselibtemplate is the library surface generated from mise-go-template.
package miselibtemplate

// StartsWith reports whether haystack begins with needle.
func StartsWith(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	return haystack[:len(needle)] == needle
}

// EndsWith reports whether haystack ends with needle.
func EndsWith(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	return haystack[len(haystack)-len(needle):] == needle
}
