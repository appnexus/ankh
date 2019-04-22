package kubectl

import (
	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
)

type ExecStage struct {
	GenericStage
}

func NewExecStage() plan.Stage {
	return &KubectlRunner{kubectl: &ExecStage{}}
}

func (stage *ExecStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	cmd.AddArguments([]string{"exec", "-it"})
	// Exec is interactive, so use stdin, stdout and stderr.
	// TODO: This shouldn't always have to be the case
	cmd.PipeStdin = plan.PIPE_TYPE_STD
	cmd.PipeStdoutAndStderr = plan.PIPE_TYPE_STD
	return cmd
}

func (stage *ExecStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	return getPodAndContainerSelection(ctx, input)
}

func (stage *ExecStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	args := ctx.ExtraArgs
	if len(ctx.PassThroughArgs) > 0 {
		passThroughArgs := append([]string{"--"}, ctx.PassThroughArgs...)
		args = append(args, passThroughArgs...)
	}
	return args
}
