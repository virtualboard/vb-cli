package main

import (
	"fmt"
	"os"

	"github.com/virtualboard/vb-cli/cmd"
)

func main() {
	os.Exit(run())
}

func run() int {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		if code := cmd.ExitCode(err); code != 0 {
			return code
		}
		return 1
	}
	return 0
}
