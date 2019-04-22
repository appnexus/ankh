package plan

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/appnexus/ankh/context"
)

type PipeType int

const (
	// Pipe should be the default, and std/none should be explicitly opted in.
	//
	// The Run() command takes input as paramater, and returns output
	// as a return value. It stands to reason that the expected default
	// behavior for this interface is to use a pipe to send stdin and
	// capture stdout/stderr.
	PIPE_TYPE_PIPE PipeType = 0
	PIPE_TYPE_STD  PipeType = 1
	PIPE_TYPE_NONE PipeType = 2
)

type Command struct {
	command                        string
	args                           []string
	PipeStdin, PipeStdoutAndStderr PipeType
}

func NewCommand(command string) Command {
	return Command{command: command}
}

func (cmd *Command) Explain() string {
	return strings.Join(append([]string{cmd.command}, cmd.args...), " ")
}

func (cmd *Command) Run(ctx *ankh.ExecutionContext, input *string) (string, error) {
	execCommand := exec.Command(cmd.command, cmd.args...)

	// Set up pipes if necessary, or use stdin/out/err.
	var stdoutPipe io.ReadCloser
	var stderrPipe io.ReadCloser
	var stdinPipe io.WriteCloser

	switch cmd.PipeStdin {
	case PIPE_TYPE_PIPE:
		if input != nil {
			stdinPipe, _ = execCommand.StdinPipe()
		}
	case PIPE_TYPE_STD:
		execCommand.Stdin = os.Stdin
	}

	switch cmd.PipeStdoutAndStderr {
	case PIPE_TYPE_PIPE:
		stdoutPipe, _ = execCommand.StdoutPipe()
		stderrPipe, _ = execCommand.StderrPipe()
	case PIPE_TYPE_STD:
		execCommand.Stdout = os.Stdout
		execCommand.Stderr = os.Stderr
	}

	err := execCommand.Start()
	if err != nil {
		return "", fmt.Errorf("error starting the '%v' command: %v", cmd.command, err)
	}

	var stdout, stderr []byte
	if stdinPipe != nil {
		// This is implicitly not-nil because we only open pipes for non-nil input. Dangerous.
		stdinPipe.Write([]byte(*input))
		stdinPipe.Close()
	}
	var wg sync.WaitGroup
	if stdoutPipe != nil {
		wg.Add(1)
		go func() {
			stdout, _ = ioutil.ReadAll(stdoutPipe)
			wg.Done()
		}()
	}
	if stderrPipe != nil {
		wg.Add(1)
		go func() {
			stderr, _ = ioutil.ReadAll(stderrPipe)
			wg.Done()
		}()
	}

	wg.Wait()

	// Catch signals while running the command, if our context demands it.
	if ctx.ShouldCatchSignals {
		ctx.CatchSignals = true
	}

	ctx.Logger.Debugf("Running command %+v", execCommand)
	err = execCommand.Wait()

	// No need to catch signals anymore, since the command has finished.
	if ctx.ShouldCatchSignals {
		ctx.CatchSignals = false
	}

	if err != nil {
		ctx.Logger.Debugf("Command finished with err %+v", err)
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			if waitStatus == 2 {
				fmt.Println("\n...interrupted")
				return "", nil
			}
			if waitStatus == 256 && ctx.Mode == ankh.Pods {
				fmt.Printf("\n...got exit code 1 from %v "+
					"(this is benign when interrupting a watch via -w)\n", cmd.command)
				return "", nil
			}
		}
		outputMsg := ""
		if len(stderr) > 0 {
			outputMsg = fmt.Sprintf(" -- the %v process had the following output on stderr:\n%s", cmd.command, stderr)
		}
		return "", fmt.Errorf("error running the %v command: %v%v", cmd.command, err, outputMsg)
	}

	return string(stdout), nil
}

func (cmd *Command) AddArguments(args []string) {
	cmd.args = append(cmd.args, args...)
}
