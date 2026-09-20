package editors

func IsOwner(owner string) bool { return owner == "zed" || owner == "vscode" }

func Name(owner string) string {
	if owner == "vscode" {
		return "VS Code"
	}
	if owner == "browser" {
		return "Browser"
	}
	if owner == "agent" || owner == "codex" {
		return "Agent"
	}
	return "Zed"
}
