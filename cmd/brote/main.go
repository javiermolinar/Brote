package main

import (
	"encoding/json"
	"os"

	"agentdebugger/internal/cli"
)

func main() {
	result, err := cli.Run(os.Args[1:])
	if err != nil {
		output := json.NewEncoder(os.Stderr)
		output.SetIndent("", "  ")
		_ = output.Encode(map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	if result != nil {
		output := json.NewEncoder(os.Stdout)
		output.SetIndent("", "  ")
		_ = output.Encode(result)
	}
}
