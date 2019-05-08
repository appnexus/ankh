package kubectl

import (
	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"

	"fmt"
	"strings"
)

type KubectlStage interface {
	GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command
	GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error)
	GetFinalArgs(ctx *ankh.ExecutionContext) []string
}

type KubectlRunner struct {
	kubectl KubectlStage
}

type GenericStage struct {}

func (stage *GenericStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	return plan.Command{}
}

func (stage *GenericStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	return []string{}, nil
}

func (stage *GenericStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	return []string{}
}

func (stage *KubectlRunner) Execute(ctx *ankh.ExecutionContext, input *string, namespace string, wildCardLabels []string) (string, error) {
	cmd := stage.kubectl.GetCommand(ctx, namespace)

	// TODO: This is also really silly
	if input == nil {
		panic("kubectl cannot execute on nil input")
	}

	args, err := stage.kubectl.GetArgsFromInput(ctx, *input, wildCardLabels)
	if err != nil {
		return "", err
	}
	cmd.AddArguments(args)

	finalArgs := stage.kubectl.GetFinalArgs(ctx)
	cmd.AddArguments(finalArgs)

	if ctx.Mode == ankh.Explain {
		// Sweet string badnesss.
		in := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(*input), "&& \\"))
		return fmt.Sprintf("(%s) | \\\n%s", in, cmd.Explain()), nil
	}

	ctx.Logger.Debugf("Running stage %+v with cmd: %+v", stage, cmd)
	out, err := cmd.Run(ctx, input)
	if err != nil {
		return out, err
	}

	return out, err
}

func Version(ctx *ankh.ExecutionContext) (string, error) {
	cmd := plan.NewCommand(ctx.AnkhConfig.Kubectl.Command)
	cmd.AddArguments([]string{"version", "--client"})
	return cmd.Run(ctx, nil)
}

func newKubectlCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := plan.NewCommand(ctx.AnkhConfig.Kubectl.Command)

	// Add common args
	cmd.AddArguments([]string{"--context", ctx.AnkhConfig.CurrentContext.KubeContext})

	if namespace != "" {
		cmd.AddArguments([]string{"--namespace", namespace})
	}

	if ctx.KubeConfigPath != "" {
		cmd.AddArguments([]string{"--kubeconfig", ctx.KubeConfigPath})
	}

	return cmd
}
