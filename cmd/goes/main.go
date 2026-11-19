package main

import (
	"fmt"
	"os"

	"goes/eval"
)

func main() {
	args := os.Args[1:]
	moduleRoot := ""

	for len(args) > 0 && args[0] == "-m" {
		if len(args) < 2 {
			fmt.Println("Usage: goes [-m <module-root>] <path-to-go-file-or-directory>")
			os.Exit(1)
		}
		moduleRoot = args[1]
		args = args[2:]
	}

	if len(args) < 1 {
		fmt.Println("Usage: goes [-m <module-root>] <path-to-go-file-or-directory>")
		os.Exit(1)
	}

	path := args[0]
	result, err := eval.RunPathWithModuleRoot(path, moduleRoot)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(result.Int())
}