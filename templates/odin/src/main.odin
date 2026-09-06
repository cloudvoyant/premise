package main

import "core:fmt"

// VERSION is injected at compile time via `-define:VERSION=$VERSION` (see mise-tasks/build/*).
// A plain `odin run src` with no -define falls back to "dev".
VERSION :: #config(VERSION, "dev")

main :: proc() {
	fmt.println("Hello from mise-lib-template!")
	fmt.printf("version: %s\n", VERSION)
	fmt.printf("starts_with(\"hello\", \"he\"): %t\n", starts_with("hello", "he"))
}
