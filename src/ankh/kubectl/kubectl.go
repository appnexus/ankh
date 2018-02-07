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

func Execute(ctx *ankh.ExecutionContext, act action, input string, ankhFile ankh.AnkhFile,
	cmd func(name string, arg ...string) *exec.Cmd) (string, error) {

	if cmd == nil {
		cmd = exec.Command
	}

	kubectlArgs := []string{
		"kubectl", string(act),
		"--context", ctx.AnkhConfig.CurrentContext.KubeContext,
	}

	if ankhFile.Namespace != "" {
		kubectlArgs = append(kubectlArgs, []string{"--namespace", ankhFile.Namespace}...)
	}

	if ctx.KubeConfigPath != "" {
		kubectlArgs = append(kubectlArgs, []string{"--kubeconfig", ctx.KubeConfigPath}...)
	}

	if ctx.DryRun {
		kubectlArgs = append(kubectlArgs, "--dry-run")
	}

	kubectlArgs = append(kubectlArgs, "-f", "-")
	kubectlCmd := cmd(kubectlArgs[0], kubectlArgs[1:]...)

	if ctx.Verbose {
		ctx.Logger.Infof("running kubectl command: %v", kubectlArgs)
	}

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
