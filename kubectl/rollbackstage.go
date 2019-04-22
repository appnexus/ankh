package kubectl

import (
	"fmt"
	"strings"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
)

type RollbackStage struct {
	GenericStage
}

func NewRollbackStage() plan.Stage {
	return &KubectlRunner{kubectl: &RollbackStage{}}
}

func getDeploymentArgsFromInput(ctx *ankh.ExecutionContext, input string) ([]string, error) {
	args := []string{}

	forEachKubeObject(input, func(obj *KubeObject) bool {
		if strings.EqualFold(obj.Kind, "deployment") ||
			strings.EqualFold(obj.Kind, "statefulset") {
			args = append(args, fmt.Sprintf("%v/%v", obj.Kind, obj.Metadata.Name))
		}

		return true
	})

	ctx.Logger.Debugf("Decided to use args %+v", args)
	return args, nil
}

func (stage *RollbackStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	cmd.AddArguments([]string{"rollout", "undo"})
	return cmd
}

func (stage *RollbackStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	// Rollout undo operates on deployments and statefulsets only
	return getDeploymentArgsFromInput(ctx, input)
}

func (stage *RollbackStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	args := ctx.ExtraArgs
	if len(ctx.PassThroughArgs) > 0 {
		passThroughArgs := append([]string{"--"}, ctx.PassThroughArgs...)
		args = append(args, passThroughArgs...)
	}
	if ctx.DryRun {
		args = append(args, []string{"--dry-run"}...)
	}
	return args
}
