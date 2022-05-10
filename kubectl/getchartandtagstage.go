package kubectl

import (
	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
)

type GetChartTagStage struct {
	GenericStage
}

func NewGetChartTagStage() plan.Stage {
	return &KubectlRunner{kubectl: &GetChartTagStage{}}
}

func (stage *GetChartTagStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	cmd.AddArguments([]string{"get", "pods"})

	// We want to stream logs to stdout/stderr, since it may be watched via `-w`.
	cmd.PipeStdoutAndStderr = plan.PIPE_TYPE_PIPE
	return cmd
}

func (stage *GetChartTagStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	// Add selector args
	selectorArgs, err := getPodSelectorArgsFromInput(ctx, input)
	if err != nil {
		return []string{}, err
	}
	selectorArgs = append(selectorArgs, getWildCardLabels(ctx, wildCardLabels)...)

	return selectorArgs, nil
}

func (stage *GetChartTagStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	args := ctx.ExtraArgs
	if len(ctx.PassThroughArgs) > 0 {
		passThroughArgs := append([]string{"--"}, ctx.PassThroughArgs...)
		args = append(args, passThroughArgs...)
	}
	return args
}
