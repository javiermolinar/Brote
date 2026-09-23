package main

import (
	"agentdebugger/internal/protocol"
	"encoding/json"
	"errors"
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

	result, err := cli.Run(os.Args[1:])
	if err != nil {
		output := json.NewEncoder(os.Stderr)
		output.SetIndent("", "  ")
		result := map[string]any{"error": err.Error()}
		var problem *protocol.Error
		if errors.As(err, &problem) {
			result["code"] = problem.Code
			result["version"] = protocol.Version
		}
		_ = output.Encode(result)
		os.Exit(1)
	}
	if result != nil {
		output := json.NewEncoder(os.Stdout)
		output.SetIndent("", "  ")
		_ = output.Encode(result)
	}
}
