package kubectl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
	"github.com/appnexus/ankh/util"
)

type PodSelectionStage struct {
	GenericStage
}

func NewPodSelectionStage() plan.Stage {
	return &KubectlRunner{kubectl: &PodSelectionStage{}}
}

// This function is suitable for parsing the data that comes out of the pod selection phase.
func getPodAndContainerSelection(ctx *ankh.ExecutionContext, kubectlOut string) ([]string, error) {
	if len(kubectlOut) <= 1 {
		return []string{}, fmt.Errorf("No pods found for input chart")
	}

	// Split the output line by line, and then again by `|` so the user can select a pod.
	// This works in conjunction with the `go-template` `outputMode` used when selecting pods with kubectl.
	lineSelection := ""
	lines := strings.Split(strings.Trim(kubectlOut, "\n "), "\n")
	for i, _ := range lines {
		lines[i] = strings.Trim(lines[i], ", ")
	}
	if len(lines) <= 1 {
		return []string{}, fmt.Errorf("No pods found for input chart")
	} else if len(lines) > 2 {
		// Sort lines by CREATED column
		sort.Slice(lines, func(i, j int) bool {
			f1 := strings.Fields(lines[i])
			f2 := strings.Fields(lines[j])

			// We want to sort by time in decreasing order, so invert the less than operator here.
			r := strings.Compare(f1[2], f2[2])
			return r >= 0
		})
		var err error
		if ctx.NoPrompt {
			// The first line is the header, so the "pod" is lines[1].
			lineSelection = lines[1]
			ctx.Logger.Warnf("Selecting first pod (of %d) \"%v\" due to `--no-prompt`",
				len(lines), lineSelection)
		} else {
			lineSelection, err = util.PromptForSelection(lines, "Select a pod", true)
			if err != nil {
				return []string{}, err
			}
		}
	} else {
		// lines[0] is the header line. So the default choice is lines[1].
		lineSelection = lines[1]
	}

	fields := strings.Fields(lineSelection)
	podSelection := fields[0]
	containers := strings.Split(fields[3], ",")

	// It's possible that container was already specified via `-c` as extra args.
	var err error
	containerSelection := ""
	containerSelected := false
	for _, extra := range ctx.ExtraArgs {
		if extra == "-c" {
			containerSelected = true
			break
		}
	}
	if !containerSelected && len(containers) > 1 {
		if ctx.NoPrompt {
			return []string{}, fmt.Errorf("Must pass a container via `-c` when using `--no-prompt`")
		}
		containerSelection, err = util.PromptForSelection(containers, "Select a container", false)
		if err != nil {
			return []string{}, err
		}
	} else {
		containerSelection = containers[0]
	}

	return []string{podSelection, "-c", containerSelection}, nil
}

func (stage *PodSelectionStage) GetCommand(ctx *ankh.ExecutionContext, namespace string) plan.Command {
	cmd := newKubectlCommand(ctx, namespace)
	cmd.AddArguments([]string{"get", "pods"})
	return cmd
}

func (stage *PodSelectionStage) GetArgsFromInput(ctx *ankh.ExecutionContext, input string, wildCardLabels []string) ([]string, error) {
	// Add output format args
	customColumns := "custom-columns=NAME:.metadata.name,STATUS:.status.phase,CREATED:.metadata.creationTimestamp,CONTAINERS:.spec.containers[*].name"
	for _, column := range wildCardLabels {
		customColumns += fmt.Sprintf(",%v:.metadata.labels.%v", strings.ToUpper(column), column)
	}
	args := []string{"-o", customColumns}

	// Add selector args
	selectorArgs, err := getPodSelectorArgsFromInput(ctx, input)
	if err != nil {
		return []string{}, err
	}

	args = append(args, selectorArgs...)
	return args, nil
}

func (stage *PodSelectionStage) GetFinalArgs(ctx *ankh.ExecutionContext) []string {
	// Pod selection is an interim state so we do not add final args (eg: no passthrough args, yet)
	return []string{}
}
