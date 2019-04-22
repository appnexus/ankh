package kubectl

import (
	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
)

type CheckStage struct {
	GenericStage
}

func NewCheckStage() plan.Stage {
	return &KubectlRunner{kubectl: &CheckStage{}}
}

func (stage *CheckStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	cmd.AddArguments([]string{"get"})
	return cmd
}

func (stage *CheckStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	// Add output format args
	args := []string{}
	args = []string{"-o", "name"}

	// Add selector args
	selectorArgs, err := getObjectArgsFromInput(ctx, input)
	if err != nil {
		return []string{}, err
	}
	selectorArgs = append(selectorArgs, getWildCardLabels(ctx, wildCardLabels)...)

	args = append(args, selectorArgs...)
	return args, nil
}

func (stage *CheckStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	return []string{}
}
