package cli

import (
	"agentdebugger/internal/protocol"
	"encoding/json"
	"errors"
	"io"
)

// Execute is the executable boundary shared by all three CLI binaries. Streaming
// handlers return nil and own their stream; ordinary results are encoded here.
func Execute(args []string, stdout, stderr io.Writer) int {
	result, err := runCLI(args, stdout)
	return writeResult(result, err, stdout, stderr)
}
func writeResult(result any, err error, stdout, stderr io.Writer) int {
	code := 0
	destination := stdout
	if err != nil {
		code = 1
		destination = stderr
		failure := obj{"error": err.Error()}
		var problem *protocol.Error
		if errors.As(err, &problem) {
			failure["code"] = problem.Code
			failure["version"] = protocol.Version
		}
		result = failure
	}
	if result != nil {
		encoder := json.NewEncoder(destination)
		encoder.SetIndent("", "  ")
		if encoder.Encode(result) != nil {
			return 1
		}
	}
	return code
}
