package main

func editorOwner(owner string) bool { return owner == "zed" || owner == "vscode" }
func editorName(owner string) string {
	if owner == "vscode" {
		return "VS Code"
	}
	return "Zed"
}
