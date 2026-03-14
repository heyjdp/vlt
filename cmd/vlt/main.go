// Package main is the entry point for the vlt secret manager.
package main

import "fmt"

var version = "dev" // overridden by ldflags

func main() {
	fmt.Println("vlt", version)
}
