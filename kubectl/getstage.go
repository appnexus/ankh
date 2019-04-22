package kubectl

import (
	"fmt"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
)

type GetStage struct {
	GenericStage
}

func NewGetStage() plan.Stage {
	return &KubectlRunner{kubectl: &GetStage{}}
}

func getWildCardLabels(ctx *ankh.ExecutionContext, wildCardLabels []string) []string {
	args := []string{}

	if !ctx.Describe {
		for _, label := range wildCardLabels {
			ctx.Logger.Debugf("Selecting %v as a label for pods", label)
			args = append(args, []string{"-L", label}...)
		}
	}

	return args
}

func getObjectArgsFromInput(ctx *ankh.ExecutionContext, input string) ([]string, error) {
	args := []string{}

	forEachKubeObject(input, func(obj *KubeObject) bool {
		args = append(args, fmt.Sprintf("%v/%v", obj.Kind, obj.Metadata.Name))
		return true
	})

	ctx.Logger.Debugf("Decided to use args %+v", args)
	return args, nil
}

func (stage *GetStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	if ctx.Describe {
		cmd.AddArguments([]string{"describe"})
	} else {
		cmd.AddArguments([]string{"get"})
	}
	return cmd
}

func (stage *GetStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	// Add output format args
	args := []string{}
	if !ctx.Describe {
		args = []string{"-o", "wide"}
	}

	// Add selector args
	selectorArgs, err := getObjectArgsFromInput(ctx, input)
	if err != nil {
		return []string{}, err
	}
	selectorArgs = append(selectorArgs, getWildCardLabels(ctx, wildCardLabels)...)

	args = append(args, selectorArgs...)
	return args, nil
}

func (stage *GetStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	args := ctx.ExtraArgs
	if len(ctx.PassThroughArgs) > 0 {
		passThroughArgs := append([]string{"--"}, ctx.PassThroughArgs...)
		args = append(args, passThroughArgs...)
	}
	return args
}
