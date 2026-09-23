package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

func validateFlags(f *flag.FlagSet, allowed string) error {
	var err error
	f.Visit(func(v *flag.Flag) {
		if !strings.Contains(" "+allowed+" ", " "+v.Name+" ") && err == nil {
			err = fmt.Errorf("flag --%s is not valid for this operation", v.Name)
		}
	})
	return err
}
func definitionFlagNames(kind, operation string) string {
	switch operation {
	case "list":
		return "client"
	case "remove":
		return "client id revision"
	default:
		flags := "client id revision file line function condition hit-condition enabled scope"
		if kind == "tracepoint" {
			flags += " name values capture-limit"
		}
		return flags
	}
}
func serviceFlagNames(verb string) string {
	switch verb {
	case "goroutines":
		return "start count"
	case "stack":
		return "goroutine start count"
	default:
		return ""
	}
}
func commentFlagNames(verb string) string {
	recipient := "recipient-kind recipient-id recipient-revision recipient-name"
	switch verb {
	case "resolve", "reopen":
		return "offline"
	case "retry":
		return "offline " + recipient
	case "import":
		return "body-file"
	case "create":
		return "workspace body-file file line run generation goroutine frame expression " + recipient
	case "ask":
		return "offline workspace context previous-session body-file run generation goroutine frame expression " + recipient
	case "reply":
		return "offline workspace question binding revision attempt body-file message-id " + recipient
	case "delivery":
		return "offline question binding revision attempt status error " + recipient
	default:
		return ""
	}
}

func newFlagSet(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}
