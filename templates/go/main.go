package main

import (
	"fmt"
	"os"

	"github.com/cloudvoyant/mise-lib-template/src/miselibtemplate"
)

// version is injected at build time via -ldflags "-X main.version=$VERSION".
var version = "dev"

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" {
			fmt.Println(version)
			return
		}
	}

	fmt.Println("Hello from mise-lib-template!")
	fmt.Printf("StartsWith(\"hello\", \"he\"): %v\n", miselibtemplate.StartsWith("hello", "he"))
}
