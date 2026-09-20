package editors

func IsOwner(owner string) bool { return owner == "zed" || owner == "vscode" }

func Name(owner string) string {
	if owner == "vscode" {
		return "VS Code"
	}
	return "Zed"
}
