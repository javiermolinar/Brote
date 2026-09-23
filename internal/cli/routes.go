package cli

import (
	"agentdebugger/internal/editors/vscode"
	"agentdebugger/internal/session"
	"agentdebugger/internal/tracing"
	"context"
	"flag"
	"fmt"
	"strings"
	"time"
)

func singleSession(fn func(string) (obj, error)) commandHandler {
	return func(args []string) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("session ID required")
		}
		return fn(args[0])
	}
}
func configsCommand(args []string) (any, error) {
	f := newFlagSet("configs")
	project := f.String("project", ".", "Project/source directory")
	path := f.String("launch-file", "", "VS Code launch.json path")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	if f.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments")
	}
	return vscode.List(*project, *path)
}
func openWorkspace([]string) (any, error) {
	endpoint, err := ensureUI()
	return obj{"panel": endpoint}, err
}
func workspaceCommand([]string) (any, error) { return session.Workspace(context.Background()) }
func savedRunCommand(args []string) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("session ID required")
	}
	return session.SavedRun(args[0])
}
func runAgainCommand(args []string) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("session ID required")
	}
	histories, err := session.ListHistory()
	if err != nil {
		return nil, err
	}
	for _, h := range histories {
		if h.ID == args[0] {
			group, err := session.InvestigationFor(h.ID)
			if err != nil {
				return nil, err
			}
			argv := []string{"--binary", h.Binary, "--project", h.Project, "--investigation", group, "--thread", "", "--"}
			argv = append(argv, h.Args...)
			settings, err := session.ReadLaunchSettings(h.Directory)
			if err != nil {
				return nil, err
			}
			return startWithLaunch(argv, settings)
		}
	}
	return nil, fmt.Errorf("saved launch configuration not found")
}
func historyCommand(args []string) (any, error) {
	switch len(args) {
	case 0:
		return session.ListHistory()
	case 1:
		return session.ReadHistory(args[0])
	default:
		return nil, fmt.Errorf("usage: history [ID]")
	}
}
func sourcesCommand(args []string) (any, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("session ID and optional FILE required")
	}
	s, err := session.Read(args[0])
	if err != nil {
		return nil, err
	}
	file := ""
	if len(args) == 2 {
		file = args[1]
	}
	return session.Sources(s, file)
}
func endSession(args []string) (any, error) {
	if len(args) != 2 || args[1] != "--confirmed" {
		return nil, fmt.Errorf("ID --confirmed required (terminates the target, regardless of owner)")
	}
	return session.End(context.Background(), args[0])
}
func versionCommand([]string) (any, error) {
	return obj{"version": Version, "protocol": 2, "capabilities": []string{"executionTasks", "taskStart", "taskDelivery", "taskExecute", "embeddedWebUI", "sharedServiceV1", "tracepoints", "sessionOTLP", "vscodeF5"}}, nil
}
func listSessions([]string) (any, error) { return session.List(context.Background()) }
func traceServe([]string) (any, error)   { return nil, tracing.Serve() }
func traceService([]string) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	endpoint, err := tracing.Ensure(ctx)
	return obj{"endpoint": endpoint}, err
}
func traceRecords([]string) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	return tracing.Records(ctx)
}
func traceQuery(args []string) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("TRACE_ID required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	return tracing.Query(ctx, args[0])
}
func queryTraces(args []string) (any, error) {
	f := newFlagSet("query traces")
	id := f.String("session", "", "Filter by stored session identity")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	seen := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "session" {
			seen = true
		}
	})
	if f.NArg() > 1 || (seen && (f.NArg() != 0 || *id == "")) {
		return nil, fmt.Errorf("use [TRACE_ID] or --session SESSION")
	}
	if f.NArg() == 1 {
		return traceQuery(f.Args())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	records, err := tracing.Records(ctx)
	if err != nil {
		return nil, err
	}
	if !seen {
		return records, nil
	}
	filtered := []tracing.Record{}
	for _, record := range records {
		if traceRecordMatchesSession(record, *id) {
			filtered = append(filtered, record)
		}
	}
	return filtered, nil
}
func commentStatus(args []string) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("SESSION THREAD open|resolved required")
	}
	verb := ""
	switch args[2] {
	case "open":
		verb = "reopen"
	case "resolved":
		verb = "resolve"
	default:
		return nil, fmt.Errorf("status must be open or resolved")
	}
	argv := append([]string{verb, args[0], args[1]}, args[3:]...)
	return commentCommandMode(argv, true)
}

// Shared-service records use ID:RunID (broker.openTrace); legacy Delve and
// native adapters retain their own exact identities. Names are never consulted.
func traceRecordMatchesSession(record tracing.Record, id string) bool {
	if record.Session == id {
		return true
	}
	sessionID, run, ok := strings.Cut(record.Session, ":")
	return record.Adapter == "brote" && ok && run != "" && sessionID == id
}
