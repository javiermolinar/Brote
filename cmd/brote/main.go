package main

import (
	"agentdebugger/internal/protocol"
	"encoding/json"
	"errors"
	"os"

	"agentdebugger/internal/cli"
)

func main() {
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
