package main

import (
	"fmt"
	"os"

	"github.com/exequieldeferrari/axiom/internal/cli"
)

func main() {
	if err := cli.Run(os.Args, os.Stdout, os.Stderr); err != nil {
		if cli.IsUsage(err) {
			os.Exit(cli.ExitCodeUsage)
		}
		fmt.Fprintf(os.Stderr, "axiom: %v\n", err)
		os.Exit(1)
	}
}
