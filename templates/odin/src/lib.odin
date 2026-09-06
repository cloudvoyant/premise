// mise_lib_template — generated from mise-odin-template.
package main

import "core:strings"
import "core:testing"

// starts_with reports whether haystack begins with needle.
starts_with :: proc(haystack, needle: string) -> bool {
	return strings.has_prefix(haystack, needle)
}

// ends_with reports whether haystack ends with needle.
ends_with :: proc(haystack, needle: string) -> bool {
	return strings.has_suffix(haystack, needle)
}

@(test)
test_starts_with :: proc(t: ^testing.T) {
	testing.expect(t, starts_with("hello world", "hello"))
	testing.expect(t, !starts_with("hello", "world"))
}

@(test)
test_ends_with :: proc(t: ^testing.T) {
	testing.expect(t, ends_with("hello world", "world"))
	testing.expect(t, !ends_with("hello", "world"))
}
