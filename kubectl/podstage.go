package kubectl

import (
	"fmt"
	"strings"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
)

type PodStage struct {
	GenericStage
}

func NewPodStage() plan.Stage {
	return &KubectlRunner{kubectl: &PodStage{}}
}

func getPodSelectorArgsFromInput(ctx *ankh.ExecutionContext, input string) ([]string, error) {
	args := []string{}
	matchLabels := make(map[string][]string)

	forEachKubeObject(input, func(obj *KubeObject) bool {
		if !strings.EqualFold(obj.Kind, "deployment") && !strings.EqualFold(obj.Kind, "statefulset") {
			return true
		}

		ctx.Logger.Debugf("Using obj %+v", obj)
		for k, v := range obj.Spec.Selector.MatchLabels {
			matchLabels[k] = append(matchLabels[k], v)
		}

		return true
	})

	if len(matchLabels) == 0 {
		return []string{}, fmt.Errorf("No Deployments or StatefulSets found for input chart")
	}

	constraints := []string{}
	for k, v := range matchLabels {
		c := fmt.Sprintf("%v in (%v)", k, strings.Join(v, ","))
		constraints = append(constraints, c)
	}
	args = append(args, []string{"-l", strings.Join(constraints, ",")}...)

	ctx.Logger.Debugf("Decided to use args %+v", args)
	return args, nil
}

func (stage *PodStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	if ctx.Describe {
		cmd.AddArguments([]string{"describe", "pods"})
	} else {
		cmd.AddArguments([]string{"get", "pods"})
	}
	// We want to stream logs to stdout/stderr, since it may be watched via `-w`.
	cmd.PipeStdoutAndStderr = plan.PIPE_TYPE_STD
	return cmd
}

func (stage *PodStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	// Add output format args
	args := []string{}
	if !ctx.Describe {
		args = []string{"-o", "wide"}
	}

	// Add selector args
	selectorArgs, err := getPodSelectorArgsFromInput(ctx, input)
	if err != nil {
		return []string{}, err
	}
	selectorArgs = append(selectorArgs, getWildCardLabels(ctx, wildCardLabels)...)

	args = append(args, selectorArgs...)
	return args, nil
}

func (stage *PodStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	args := ctx.ExtraArgs
	if len(ctx.PassThroughArgs) > 0 {
		passThroughArgs := append([]string{"--"}, ctx.PassThroughArgs...)
		args = append(args, passThroughArgs...)
	}
	return args
}
