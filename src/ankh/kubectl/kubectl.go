package kubectl

import (
	"fmt"
	"io/ioutil"
	"os/exec"

	"ankh"
)

type action string

const (
	Apply  action = "apply"
	Delete action = "delete"
)

func Execute(ctx *ankh.ExecutionContext, act action, dryRun bool, input string, ankhFile ankh.AnkhFile, ankhConfig ankh.AnkhConfig) (string, error) {
	kubectlArgs := []string{
		"kubectl", string(act),
		"--context", ankhConfig.CurrentContext.KubeContext,
		"--namespace", ankhFile.Namespace,
	}

	if ctx.KubeConfig != "" {
		kubectlArgs = append(kubectlArgs, []string{"--kubeconfig", ctx.KubeConfig}...)
	}

	if dryRun {
		kubectlArgs = append(kubectlArgs, "--dry-run")
	}

	kubectlArgs = append(kubectlArgs, "-f", "-")
	kubectlCmd := exec.Command(kubectlArgs[0], kubectlArgs[1:]...)

	kubectlStdoutPipe, _ := kubectlCmd.StdoutPipe()
	kubectlStderrPipe, _ := kubectlCmd.StderrPipe()
	kubectlStdinPipe, _ := kubectlCmd.StdinPipe()

	kubectlCmd.Start()
	kubectlStdinPipe.Write([]byte(input))
	kubectlStdinPipe.Close()

	kubectlOut, _ := ioutil.ReadAll(kubectlStdoutPipe)
	kubectlErr, _ := ioutil.ReadAll(kubectlStderrPipe)

	err := kubectlCmd.Wait()
	if err != nil {
		return "", fmt.Errorf("error running the kubectl command:\n%s", kubectlErr)
	}
	return string(kubectlOut), nil
}
