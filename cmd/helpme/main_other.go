//go:build !(windows && amd64)

package main

import "fmt"

func main() {
	fmt.Println("The HelpMe host currently runs on 64-bit Windows only. " +
		"Helpers can use any browser: https://guxos.com/helpme/")
}
