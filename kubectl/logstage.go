package kubectl

import (
	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
)

type LogStage struct {
	GenericStage
}

func NewLogStage() plan.Stage {
	return &KubectlRunner{kubectl: &LogStage{}}
}

func (stage *LogStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	cmd.AddArguments([]string{"logs"})
	// We want to stream logs to stdout/stderr, since it may be tailed via `-f`.
	// TODO: This shouldn't always have to be the case
	cmd.PipeStdoutAndStderr = plan.PIPE_TYPE_STD
	return cmd
}

func (stage *LogStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	return getPodAndContainerSelection(ctx, input)
}

func (stage *LogStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	args := ctx.ExtraArgs
	if len(ctx.PassThroughArgs) > 0 {
		passThroughArgs := append([]string{"--"}, ctx.PassThroughArgs...)
		args = append(args, passThroughArgs...)
	}
	return args
}
