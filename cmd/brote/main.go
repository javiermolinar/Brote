package main

import (
	"fmt"
	"os"

	"agentdebugger/internal/cli"
	"agentdebugger/internal/embeddedtempo"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "embedded-tempo" {
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: brote embedded-tempo DATA_DIRECTORY")
			os.Exit(2)
		}
		if err := embeddedtempo.Run(os.Args[2], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
