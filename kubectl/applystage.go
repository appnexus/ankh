package kubectl

import (
	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
)

type ApplyStage struct {
	GenericStage
}

func NewApplyStage() plan.Stage {
	return &KubectlRunner{kubectl: &ApplyStage{}}
}

func (stage *ApplyStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	cmd.AddArguments([]string{"apply"})
	// Send apply results to stdout
	cmd.PipeStdoutAndStderr = plan.PIPE_TYPE_STD
	return cmd
}

func (stage *ApplyStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	// The apply stage takes yaml from stdin, so there are no additional args beyond `-f -`
	return []string{"-f", "-"}, nil
}

func (stage *ApplyStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	args := ctx.ExtraArgs
	if len(ctx.PassThroughArgs) > 0 {
		args = append(args, append([]string{"--"}, ctx.PassThroughArgs...)...)
	}
	if ctx.DryRun {
		args = append(args, []string{"--dry-run"}...)
	}
	return args
}
