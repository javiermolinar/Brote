package cli

import (
	"flag"

	"debug-handover/internal/broker"
)

func serve(args []string) error {
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	id := f.String("id", "", "session")
	bin := f.String("binary", "", "binary")
	project := f.String("project", "", "project")
	dlv := f.String("dlv", "dlv", "Delve")
	recovering := f.Bool("recover", false, "reattach broker")
	thread := f.String("thread", "", "Codex task")
	if e := f.Parse(args); e != nil {
		return e
	}
	return broker.Serve(broker.Options{ID: *id, Binary: *bin, Project: *project, Delve: *dlv, Thread: *thread, Recover: *recovering, Args: f.Args()})
}
