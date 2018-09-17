package kubectl

import (
	"fmt"
	"io/ioutil"
	"os/exec"
	"strings"

	"github.com/appnexus/ankh/context"
)

func Version() (string, error) {
	kubectlArgs := []string{"kubectl", "version", "--client"}
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

func Execute(ctx *ankh.ExecutionContext, input string, ankhFile ankh.AnkhFile,
	cmd func(name string, arg ...string) *exec.Cmd) (string, error) {

	if cmd == nil {
		cmd = exec.Command
	}

	kubectlArgs := []string{"kubectl"}
	if ctx.Mode == ankh.Diff {
		kubectlArgs = append(kubectlArgs, []string{"alpha", "diff", "LAST", "LOCAL"}...)
	} else {
		kubectlArgs = append(kubectlArgs, []string{"apply"}...)
	}

	if ctx.AnkhConfig.CurrentContext.KubeServer != "" {
		kubectlArgs = append(kubectlArgs, []string{"--server", ctx.AnkhConfig.CurrentContext.KubeServer}...)
	} else {
		kubectlArgs = append(kubectlArgs, []string{"--context", ctx.AnkhConfig.CurrentContext.KubeContext}...)
	}

	if ankhFile.Namespace != "" {
		kubectlArgs = append(kubectlArgs, []string{"--namespace", ankhFile.Namespace}...)
	}

	if ctx.AnkhConfig.CurrentContext.KubeServer == "" {
		if ctx.KubeConfigPath != "" {
			kubectlArgs = append(kubectlArgs, []string{"--kubeconfig", ctx.KubeConfigPath}...)
		}
	}

	if ctx.DryRun {
		kubectlArgs = append(kubectlArgs, "--dry-run")
	}

	kubectlArgs = append(kubectlArgs, "-f", "-")
	kubectlCmd := cmd(kubectlArgs[0], kubectlArgs[1:]...)

	if ctx.Verbose {
		ctx.Logger.Infof("running kubectl command: %v", kubectlArgs)
	}

	if ctx.Mode == ankh.Explain {
		return strings.Join(kubectlCmd.Args, " "), nil
	}

	kubectlStdoutPipe, _ := kubectlCmd.StdoutPipe()
	kubectlStderrPipe, _ := kubectlCmd.StderrPipe()
	kubectlStdinPipe, _ := kubectlCmd.StdinPipe()

	err := kubectlCmd.Start()
	if err != nil {
		return "", fmt.Errorf("error starting the kubectl command: %v", err)
	}

	kubectlStdinPipe.Write([]byte(input))
	kubectlStdinPipe.Close()

	kubectlOut, _ := ioutil.ReadAll(kubectlStdoutPipe)
	kubectlErr, _ := ioutil.ReadAll(kubectlStderrPipe)

	err = kubectlCmd.Wait()
	if err != nil {
		outputMsg := ""
		if len(kubectlErr) > 0 {
			outputMsg = fmt.Sprintf(" -- the kubectl process had the following output on stderr:\n%s", kubectlErr)
		}
		return "", fmt.Errorf("error running the kubectl command: %v%v", err, outputMsg)
	}
	return string(kubectlOut), nil
}
