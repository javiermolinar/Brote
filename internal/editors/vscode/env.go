package vscode

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envAssignment = regexp.MustCompile(`^\s*(?:export\s+)?([\w.\-]+)\s*=\s*(.*?)\s*$`)
var envVariable = regexp.MustCompile(`\$\{([a-zA-Z]\w*)\}`)

func readEnvFile(path string) (map[string]*string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("envFile: %w", err)
	}
	if strings.ContainsRune(string(data), 0) {
		return nil, fmt.Errorf("envFile %s contains a NUL byte", path)
	}
	values := map[string]*string{}
	for _, line := range strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n") {
		match := envAssignment.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		value := match[2]
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = strings.ReplaceAll(value, `\n`, "\n")
		}
		if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
			value = value[1:]
		}
		if len(value) > 0 && (value[len(value)-1] == '"' || value[len(value)-1] == '\'') {
			value = value[:len(value)-1]
		}
		// Match vscode-go's dotenv interpolation: preceding entries in this
		// file take precedence over the launching process's environment.
		var out strings.Builder
		last := 0
		for _, span := range envVariable.FindAllStringSubmatchIndex(value, -1) {
			out.WriteString(value[last:span[0]])
			if span[0] > 0 && value[span[0]-1] == '\\' {
				out.WriteString(value[span[0]:span[1]])
			} else {
				key := value[span[2]:span[3]]
				if local := values[key]; local != nil && *local != "" {
					out.WriteString(*local)
				} else {
					out.WriteString(os.Getenv(key))
				}
			}
			last = span[1]
		}
		out.WriteString(value[last:])
		value = strings.ReplaceAll(out.String(), `\$`, "$")
		values[match[1]] = &value
	}
	return values, nil
}
