package cli

import (
	"agentdebugger/internal/broker"
)

func serve(args []string) error {
	f := newFlagSet("serve")
	editorStart := f.Bool("editor-start", false, "editor startup lease")
	attachPID := f.Int("pid", 0, "local target process")
	service := f.Bool("service", false, "authenticated shared-service mode")
	backend := f.String("backend", "dap", "debug backend")
	id := f.String("id", "", "session")
	bin := f.String("binary", "", "binary")
	project := f.String("project", "", "project")
	dlv := f.String("dlv", "dlv", "Delve")
	recovering := f.Bool("recover", false, "reattach broker")
	binding := f.String("binding", "", "client binding")
	name := f.String("name", "Agent", "agent name")
	thread := f.String("thread", "", "Codex task")
	if e := f.Parse(args); e != nil {
		return e
	}
	return broker.Serve(broker.Options{EditorStartup: *editorStart, AttachPID: *attachPID, Service: *service, Backend: *backend, ID: *id, Binary: *bin, Project: *project, Delve: *dlv, Thread: *thread, BindingID: *binding, AgentName: *name, Recover: *recovering, Args: f.Args()})
}
