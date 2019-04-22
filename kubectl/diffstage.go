package kubectl

import (
	"os"
	"strings"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
)

type DiffStage struct {
	GenericStage
}

func NewDiffStage() plan.Stage {
	return &KubectlRunner{kubectl: &DiffStage{}}
}

func (stage *DiffStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	diffCommand := os.Getenv("ANKH_DIFF_COMMAND")
	if diffCommand != "" {
		cmd.AddArguments(strings.Fields(diffCommand))
	} else {
		cmd.AddArguments([]string{"alpha", "diff", "LAST", "LOCAL"})
	}
	return cmd
}

func (stage *DiffStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	// The apply stage takes yaml from stdin, so there are no additional args beyond `-f -`
	return []string{"-f", "-"}, nil
}

func (stage *DiffStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	finalArgs := ctx.ExtraArgs
	if len(ctx.PassThroughArgs) > 0 {
		finalArgs = append(finalArgs, append([]string{"--"}, ctx.PassThroughArgs...)...)
	}
	return finalArgs
}
