package vscode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type expander struct{ root, file string }

func (e expander) string(value string) (string, error) {
	var out strings.Builder
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			out.WriteString(value)
			break
		}
		out.WriteString(value[:start])
		value = value[start+2:]
		end := strings.IndexByte(value, '}')
		if end < 0 {
			return "", fmt.Errorf("unterminated variable")
		}
		name := value[:end]
		replacement, err := e.variable(name)
		if err != nil {
			return "", err
		}
		out.WriteString(replacement) // Substitution is deliberately not recursive.
		value = value[end+1:]
	}
	if strings.ContainsRune(out.String(), 0) {
		return "", fmt.Errorf("NUL bytes are not supported")
	}
	return out.String(), nil
}

func (e expander) variable(name string) (string, error) {
	switch name {
	case "workspaceFolder", "workspaceRoot":
		return e.root, nil
	case "workspaceFolderBasename":
		return filepath.Base(e.root), nil
	case "userHome":
		return os.UserHomeDir()
	case "cwd":
		return os.Getwd()
	case "pathSeparator", "/":
		return string(filepath.Separator), nil
	}
	if strings.HasPrefix(name, "env:") {
		return os.Getenv(strings.TrimPrefix(name, "env:")), nil
	}
	if name == "workspaceFolder:"+filepath.Base(e.root) {
		return e.root, nil
	}
	switch name {
	case "file", "fileDirname", "fileBasename", "fileBasenameNoExtension", "fileExtname", "relativeFile", "relativeFileDirname":
		if e.file == "" {
			return "", fmt.Errorf("${%s} needs an active file; pass --file PATH", name)
		}
		switch name {
		case "file":
			return e.file, nil
		case "fileDirname":
			return filepath.Dir(e.file), nil
		case "fileBasename":
			return filepath.Base(e.file), nil
		case "fileBasenameNoExtension":
			return strings.TrimSuffix(filepath.Base(e.file), filepath.Ext(e.file)), nil
		case "fileExtname":
			return filepath.Ext(e.file), nil
		default:
			relative, err := filepath.Rel(e.root, e.file)
			if name == "relativeFileDirname" {
				relative = filepath.Dir(relative)
			}
			return relative, err
		}
	}
	return "", fmt.Errorf("unsupported VS Code variable ${%s}; replace it with a literal value", name)
}

// splitArgs understands quoting without invoking a shell or expanding commands.
func splitArgs(value string) ([]string, error) {
	var result []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, c := range value {
		switch {
		case escaped:
			if quote == '"' && c != '"' && c != '\\' && c != '$' && c != '`' && c != '\n' {
				word.WriteRune('\\')
			}
			word.WriteRune(c)
			escaped = false
		case c == '\\' && quote != '\'':
			escaped, started = true, true
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, started = c, true
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			if started {
				result = append(result, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(c)
			started = true
		}
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("unclosed quote or escape in argument string; use an array for literal arguments")
	}
	if started {
		result = append(result, word.String())
	}
	return result, nil
}
