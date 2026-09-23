package broker

import (
	"agentdebugger/internal/delve"
	"agentdebugger/internal/session"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func startTarget(s *session.Descriptor, options Options, settings *session.LaunchSettings) (*exec.Cmd, error) {
	logName := "delve.log"
	if s.RunID != "" {
		logName = "delve-" + s.RunID + ".log"
	}
	log, e := os.OpenFile(filepath.Join(s.Dir, logName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return nil, e
	}
	defer log.Close()
	argv := append([]string{"exec", s.Binary, "--headless", "--listen=127.0.0.1:0", "--api-version=2", "--accept-multiclient", "--"}, options.Args...)
	if options.AttachPID > 0 {
		argv = []string{"attach", strconv.Itoa(options.AttachPID), "--headless", "--listen=127.0.0.1:0", "--api-version=2", "--accept-multiclient"}
	}
	process := exec.Command(options.Delve, argv...)
	process.Dir = s.Project
	if settings != nil {
		if settings.Cwd != "" {
			process.Dir = settings.Cwd
		}
		process.Env = settings.Environment(process.Environ())
	}
	process.Stdout = log
	process.Stderr = log
	// Delve has its own session and a file-backed log, so a broker crash does not
	// sever its stdout pipe or deliver a terminal signal to the target.
	process.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = process.Start(); e != nil {
		return nil, e
	}
	s.DelvePID = process.Process.Pid
	success := false
	defer func() {
		if !success {
			if s.Attached && s.RPC != "" {
				_, _ = delve.Call(s.RPC, "Detach", obj{"Kill": false}, 3*time.Second)
			}
			_ = process.Process.Kill()
			_ = process.Wait()
		}
	}()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		data, _ := os.ReadFile(filepath.Join(s.Dir, logName))
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "API server listening at: ") {
				s.RPC = strings.TrimSpace(strings.TrimPrefix(line, "API server listening at: "))
				break
			}
		}
		if s.RPC != "" {
			break
		}
	}
	if s.RPC == "" {
		return nil, fmt.Errorf("Delve did not start; see %s", filepath.Join(s.Dir, logName))
	}
	success = true
	return process, nil
}
