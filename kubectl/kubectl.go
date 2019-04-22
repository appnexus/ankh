package kubectl

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
)

func Version(ctx *ankh.ExecutionContext) (string, error) {
	kubectlArgs := []string{ctx.AnkhConfig.Kubectl.Command, "version", "--client"}
	kubectlCmd := exec.Command(kubectlArgs[0], kubectlArgs[1:]...)
	kubectlOutput, err := kubectlCmd.CombinedOutput()
	if err != nil {
		outputMsg := ""
		if len(kubectlOutput) > 0 {
			outputMsg = fmt.Sprintf(" -- the kubectl process had the following output on stdout/stderr:\n%s", kubectlOutput)
		}
		return "", fmt.Errorf("%v%v", err, outputMsg)
	}
	return string(kubectlOutput), nil
}

type KubeObject struct {
	Kind     string
	Metadata struct {
		Name   string
		Labels map[string]string
	}
	Spec struct {
		Selector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		}
	}
}

func getPodMatchLabelsFromInput(ctx *ankh.ExecutionContext, input string,
	wildCardLabels []string, showWildCardLabels bool) ([]string, error) {
	args := []string{}
	matchLabels := make(map[string][]string)
	decoder := yaml.NewDecoder(strings.NewReader(input))

	for {
		obj := KubeObject{}
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}

		if !strings.EqualFold(obj.Kind, "deployment") && !strings.EqualFold(obj.Kind, "statefulset") {
			continue
		}

		ctx.Logger.Debugf("Using obj %+v", obj)
		for k, v := range obj.Spec.Selector.MatchLabels {
			if !util.Contains(wildCardLabels, k) {
				matchLabels[k] = append(matchLabels[k], v)
			}
		}
	}

	if len(matchLabels) == 0 {
		return []string{}, fmt.Errorf("No Deployments or StatefulSets found for input chart")
	}

	constraints := []string{}
	for k, v := range matchLabels {
		c := fmt.Sprintf("%v in (%v)", k, strings.Join(v, ","))
		constraints = append(constraints, c)
	}
	args = append(args, []string{"-l", strings.Join(constraints, ",")}...)

	if showWildCardLabels {
		for _, label := range wildCardLabels {
			ctx.Logger.Debugf("Selecting %v as a label instead", label)
			args = append(args, []string{"-L", label}...)
		}
	}

	ctx.Logger.Debugf("Decided to use args %+v", args)
	return args, nil
}

func getObjectArgsFromInput(ctx *ankh.ExecutionContext, input string,
	wildCardLabels []string, showWildCardLabels bool) ([]string, error) {
	args := []string{}
	decoder := yaml.NewDecoder(strings.NewReader(input))

	for {
		obj := KubeObject{}
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}

		ctx.Logger.Debugf("Decoded a kube object with kind '%v'", obj.Kind)
		if obj.Kind == "" {
			// Ignore empty documents
			ctx.Logger.Debugf("Skipping empty document")
			continue
		}

		ctx.Logger.Debugf("Using obj %+v", obj)
		args = append(args, fmt.Sprintf("%v/%v", obj.Kind, obj.Metadata.Name))
	}

	if showWildCardLabels {
		for _, label := range wildCardLabels {
			ctx.Logger.Debugf("Selecting %v as a label instead", label)
			args = append(args, []string{"-L", label}...)
		}
	}

	ctx.Logger.Debugf("Decided to use args %+v", args)

	return args, nil
}

func getSelectorArgsFromInput(ctx *ankh.ExecutionContext, input string,
	wildCardLabels []string, showWildCardLabels bool) ([]string, error) {
	args := []string{}
	kindMap := make(map[string]string)
	selectorLabels := make(map[string][]string)
	showLabels := make(map[string]string)
	decoder := yaml.NewDecoder(strings.NewReader(input))

	for {
		obj := KubeObject{}
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}

		ctx.Logger.Debugf("Decoded a kube object with kind '%v'", obj.Kind)
		if obj.Kind == "" {
			// Ignore empty documents
			ctx.Logger.Debugf("Skipping empty document")
			continue
		}

		kindMap[obj.Kind] = obj.Kind

		// Also get pods and replicasets, since these objects likely aren't in
		// the helm chart but end up being very useful.
		if kindMap["deployment"] == "" &&
			(strings.EqualFold(obj.Kind, "deployment") ||
				strings.EqualFold(obj.Kind, "statefulset")) {
			kindMap["pod"] = "pod"
			kindMap["replicaset"] = "replicaset"
			for k, v := range obj.Metadata.Labels {
				if util.Contains(wildCardLabels, k) {
					ctx.Logger.Debugf("Skipping wildcard label %v as label constraint", k)
					showLabels[k] = k
				} else {
					selectorLabels[k] = append(selectorLabels[k], v)
				}
			}
		}
	}

	constraints := []string{}
	for k, v := range selectorLabels {
		c := fmt.Sprintf("%v in (%v)", k, strings.Join(v, ","))
		constraints = append(constraints, c)
	}
	args = append(args, []string{"-l", strings.Join(constraints, ",")}...)

	kinds := []string{}
	for _, k := range kindMap {
		kinds = append(kinds, k)
	}
	args = append(args, strings.Join(kinds, ","))

	if showWildCardLabels {
		for _, label := range showLabels {
			ctx.Logger.Debugf("Selecting %v as a label instead", label)
			args = append(args, []string{"-L", label}...)
		}
	}

	ctx.Logger.Debugf("Decided to use args %+v", args)
	return args, nil
}

func kubectlExec(ctx *ankh.ExecutionContext, kubectlCmd *exec.Cmd, input string,
	skipStdin bool, skipStdoutAndStderr bool) (string, error) {
	var kubectlStdoutPipe io.ReadCloser
	var kubectlStderrPipe io.ReadCloser
	var kubectlStdinPipe io.WriteCloser
	if !skipStdoutAndStderr {
		kubectlStdoutPipe, _ = kubectlCmd.StdoutPipe()
		kubectlStderrPipe, _ = kubectlCmd.StderrPipe()
	} else {
		kubectlCmd.Stdout = os.Stdout
		kubectlCmd.Stderr = os.Stderr
	}
	if !skipStdin {
		kubectlStdinPipe, _ = kubectlCmd.StdinPipe()
	} else {
		kubectlCmd.Stdin = os.Stdin
	}

	// Sometimes, We want to catch signals while running kubectl.
	// We only set ctx.ShouldCatchSignals for operations that we want
	// the user to interrupt gracefully (eg logs -f) but not for
	// everything (eg apply)
	if ctx.ShouldCatchSignals {
		ctx.Logger.Debugf("Setting ctx.CatchSignals=true ...")
		ctx.CatchSignals = true
		defer func() {
			ctx.Logger.Debugf("Setting ctx.CatchSignals=false ...")
			ctx.CatchSignals = false
		}()
	}

	err := kubectlCmd.Start()
	if err != nil {
		return "", fmt.Errorf("error starting the kubectl command: %v", err)
	}

	if !skipStdin {
		kubectlStdinPipe.Write([]byte(input))
		kubectlStdinPipe.Close()
	}

	var kubectlOut, kubectlErr []byte
	if !skipStdoutAndStderr {
		// Read stdout and stderr simultaneously to prevent blocking when
		// os.Stdout buffer is full
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			kubectlOut, _ = ioutil.ReadAll(kubectlStdoutPipe)
			wg.Done()
		}()
		kubectlErr, _ = ioutil.ReadAll(kubectlStderrPipe)

		wg.Wait()
	}

	ctx.Logger.Debugf("Running kubectl cmd %+v", kubectlCmd)
	err = kubectlCmd.Wait()
	ctx.Logger.Debugf("Kubectl command finished with err %+v", err)
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus := exitError.Sys().(syscall.WaitStatus)
			if waitStatus == 2 {
				fmt.Println("\n...interrupted")
				return "", nil
			}
			if waitStatus == 256 && (ctx.Mode == ankh.Get || ctx.Mode == ankh.Pods) {
				fmt.Println("\n...got exit code 1 from kubectl " +
					"(this is benign when interrupting a watch via -w)")
				return "", nil
			}
		}
		outputMsg := ""
		if len(kubectlErr) > 0 {
			outputMsg = fmt.Sprintf(" -- the kubectl process had the following output on stderr:\n%s", kubectlErr)
		}
		return "", fmt.Errorf("error running the kubectl command: %v%v", err, outputMsg)
	}

	return string(kubectlOut), nil
}

func kubectlCommonArgs(ctx *ankh.ExecutionContext, namespace string) []string {
	kubectlArgs := []string{"--context", ctx.AnkhConfig.CurrentContext.KubeContext}

	if namespace != "" {
		kubectlArgs = append(kubectlArgs, []string{"--namespace", namespace}...)
	}

	if ctx.KubeConfigPath != "" {
		kubectlArgs = append(kubectlArgs, []string{"--kubeconfig", ctx.KubeConfigPath}...)
	}

	if ctx.DryRun {
		kubectlArgs = append(kubectlArgs, "--dry-run")
	}

	return kubectlArgs
}

func Execute(ctx *ankh.ExecutionContext, input string, namespace string, wildCardLabels []string,
	cmd func(name string, arg ...string) *exec.Cmd) (string, error) {
	skipStdin := false
	skipStdoutAndStderr := false
	if cmd == nil {
		cmd = exec.Command
	}

	kubectlArgs := []string{ctx.AnkhConfig.Kubectl.Command}
	switch ctx.Mode {
	case ankh.Diff:
		kubectlArgs = append(kubectlArgs, []string{"alpha", "diff", "LAST", "LOCAL"}...)
	case ankh.Logs:
		fallthrough // We treat logs commands like a "get" until we choose a pod to get logs for
	case ankh.Exec:
		fallthrough // We treat exec commands like a "get" until we choose a pod to call exec on
	case ankh.Pods:
		fallthrough // Pods is just a `get`.
	case ankh.Get:
		verb := "get"
		if ctx.Describe {
			verb = "describe"
		}
		kubectlArgs = append(kubectlArgs, verb)
	case ankh.Rollback:
		kubectlArgs = append(kubectlArgs, []string{"rollout", "undo"}...)
	case ankh.Explain:
		fallthrough
	case ankh.Apply:
		kubectlArgs = append(kubectlArgs, []string{"apply"}...)
	default:
		panic(fmt.Sprintf("Missing case handler for mode %v!", ctx.Mode))
	}

	// Decide if we should use selectors for input args instead of stdin
	outputMode := []string{}
	if !ctx.Describe {
		outputMode = []string{"-o", "wide"}
	}
	switch ctx.Mode {
	case ankh.Exec:
		fallthrough
	case ankh.Logs:
		customColumns := "custom-columns=NAME:.metadata.name,STATUS:.status.phase,CREATED:.metadata.creationTimestamp,CONTAINERS:.spec.containers[*].name"
		for _, column := range wildCardLabels {
			customColumns += fmt.Sprintf(",%v:.metadata.labels.%v", strings.ToUpper(column), column)
		}
		outputMode = []string{"-o", customColumns}
		fallthrough
	case ankh.Pods:
		showWildCardLabels := false
		if ctx.Mode == ankh.Pods {
			// TODO: Clean this all up.
			skipStdoutAndStderr = true
			showWildCardLabels = !ctx.Describe
		}
		kubectlArgs = append(kubectlArgs, append([]string{"pods"}, outputMode...)...)
		args, err := getPodMatchLabelsFromInput(ctx, input, wildCardLabels, showWildCardLabels)
		if err != nil {
			return "", err
		}
		kubectlArgs = append(kubectlArgs, args...)
		skipStdin = true
	case ankh.Get:
		skipStdoutAndStderr = true
		args, err := getObjectArgsFromInput(ctx, input, wildCardLabels, !ctx.Describe)
		if err != nil {
			return "", err
		}
		kubectlArgs = append(kubectlArgs, args...)
		skipStdin = true
	default:
		kubectlArgs = append(kubectlArgs, "-f", "-")
	}

	commonArgs := kubectlCommonArgs(ctx, namespace)
	kubectlArgs = append(kubectlArgs, commonArgs...)
	switch ctx.Mode {
	case ankh.Logs:
		// Extra args for `logs` etc come later, after we do the initial `get`.
		fallthrough
	case ankh.Exec:
		break
	default:
		kubectlArgs = append(kubectlArgs, ctx.ExtraArgs...)
		if len(ctx.PassThroughArgs) > 0 {
			kubectlArgs = append(kubectlArgs, append([]string{"--"}, ctx.PassThroughArgs...)...)
		}
	}
	kubectlCmd := cmd(kubectlArgs[0], kubectlArgs[1:]...)

	if ctx.Mode == ankh.Explain {
		return strings.Join(kubectlCmd.Args, " "), nil
	}

	kubectlOut, err := kubectlExec(ctx, kubectlCmd, input, skipStdin, skipStdoutAndStderr)
	if err != nil {
		return kubectlOut, err
	}

	switch ctx.Mode {
	case ankh.Exec:
		fallthrough
	case ankh.Logs:
		if len(kubectlOut) <= 1 {
			suggestion := ""
			if namespace == "" {
				suggestion = " (did you forget to specify a namespace using -n/--namespace?)"
			}
			return "", fmt.Errorf("No pods found for input chart in namespace \"%v\"%v",
				namespace, suggestion)
		}

		// Split the output line by line, and then again by `|` so the user can select a pod.
		// This works in conjunction with the `go-template` `outputMode` used when selecting pods with kubectl.
		lineSelection := ""
		lines := strings.Split(strings.Trim(kubectlOut, "\n "), "\n")
		for i, _ := range lines {
			lines[i] = strings.Trim(lines[i], ", ")
		}
		if len(lines) > 1 {
			// Sort lines by CREATED column
			sort.Slice(lines, func(i, j int) bool {
				f1 := strings.Fields(lines[i])
				f2 := strings.Fields(lines[j])

				// We want to sort by time in decreasing order, so invert the less than operator here.
				r := strings.Compare(f1[2], f2[2])
				return r >= 0
			})
			if ctx.NoPrompt {
				lineSelection = lines[0]
				ctx.Logger.Warnf("Selecting first pod (of %d) \"%v\" due to `--no-prompt`",
					len(lines), lineSelection)
			} else {
				lineSelection, err = util.PromptForSelection(lines, "Select a pod", true)
				if err != nil {
					return "", err
				}
			}
		} else {
			lineSelection = lines[0]
		}

		fields := strings.Fields(lineSelection)
		podSelection := fields[0]
		containers := strings.Split(fields[3], ",")

		// It's possible that container was already specified via `-c` as extra args.
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
				return "", fmt.Errorf("Must pass a container via `-c` when using `--no-prompt`")
			}
			containerSelection, err = util.PromptForSelection(containers, "Select a container", false)
			if err != nil {
				return "", err
			}
		} else {
			containerSelection = containers[0]
		}

		// We need to call kubectl again, given a pod argument chosen by the user.
		kubectlArgs := []string{}
		switch ctx.Mode {
		case ankh.Exec:
			kubectlArgs = append(kubectlArgs, []string{ctx.AnkhConfig.Kubectl.Command, "exec", "-it"}...)
		case ankh.Logs:
			kubectlArgs = append(kubectlArgs, []string{ctx.AnkhConfig.Kubectl.Command, "logs"}...)
		}
		kubectlArgs = append(kubectlArgs, commonArgs...)
		kubectlArgs = append(kubectlArgs, ctx.ExtraArgs...)
		kubectlArgs = append(kubectlArgs, podSelection)
		kubectlArgs = append(kubectlArgs, []string{"-c", containerSelection}...)
		if len(ctx.PassThroughArgs) > 0 {
			kubectlArgs = append(kubectlArgs, append([]string{"--"}, ctx.PassThroughArgs...)...)
		}
		kubectlCmd := cmd(kubectlArgs[0], kubectlArgs[1:]...)
		return kubectlExec(ctx, kubectlCmd, "", true, true)
	default:
		return string(kubectlOut), nil
	}
}
