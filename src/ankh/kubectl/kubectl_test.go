package kubectl

import (
	"ankh"
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const KUBE_CONFIG_PATH = "/path/to/config"
const KUBE_CONTEXT = "test_context"
const NAMESPACE = "test_namespace"
const INPUT = "test yaml"

func newCtx() *ankh.ExecutionContext {
	return &ankh.ExecutionContext{
		KubeConfigPath: KUBE_CONFIG_PATH,
		AnkhConfig: ankh.AnkhConfig{
			CurrentContext: ankh.Context{
				KubeContext: KUBE_CONTEXT,
			},
		},
	}
}

// Mocked exec.Command to test errors
func execCommandContextFail(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "FAIL=1"}
	return cmd
}

// Mocked exec.Command
func execCommandContext(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	// Test exec.Command error
	if os.Getenv("FAIL") == "1" {
		fmt.Fprintf(os.Stderr, "Ooops!")
		os.Exit(1)
	}

	// Mocked exec.Command will return input command + stdin
	output := strings.Join(os.Args[3:], " ")
	reader := bufio.NewReader(os.Stdin)
	stdin, _ := reader.ReadString('\n')
	output += " " + stdin

	fmt.Fprintf(os.Stdout, output)
	os.Exit(0)
}

func TestExecute(t *testing.T) {
	t.Run("apply", func(t *testing.T) {
		ctx := newCtx()
		config := ankh.AnkhFile{
			Namespace: NAMESPACE,
		}

		output, err := Execute(ctx, Apply, INPUT, config, execCommandContext)

		if err != nil {
			t.Error(err)
		}
		expected := fmt.Sprintf("kubectl apply --context %s --namespace %s --kubeconfig %s -f - %s",
			KUBE_CONTEXT, NAMESPACE, KUBE_CONFIG_PATH, INPUT)

		if output != expected {
			t.Errorf("Expected command: %s; found: %s", expected, output)
		}
	})

	t.Run("delete", func(t *testing.T) {
		ctx := newCtx()
		config := ankh.AnkhFile{
			Namespace: NAMESPACE,
		}

		output, err := Execute(ctx, Delete, INPUT, config, execCommandContext)

		if err != nil {
			t.Error(err)
		}
		expected := fmt.Sprintf("kubectl delete --context %s --namespace %s --kubeconfig %s -f - %s",
			KUBE_CONTEXT, NAMESPACE, KUBE_CONFIG_PATH, INPUT)

		if output != expected {
			t.Errorf("Expected command: %s; found: %s", expected, output)
		}
	})

	t.Run("apply dry run", func(t *testing.T) {
		ctx := newCtx()
		ctx.DryRun = true
		config := ankh.AnkhFile{
			Namespace: NAMESPACE,
		}

		output, err := Execute(ctx, Apply, INPUT, config, execCommandContext)

		if err != nil {
			t.Error(err)
		}
		expected := fmt.Sprintf("kubectl apply --context %s --namespace %s --kubeconfig %s --dry-run -f - %s",
			KUBE_CONTEXT, NAMESPACE, KUBE_CONFIG_PATH, INPUT)

		if output != expected {
			t.Errorf("Expected command: %s; found: %s", expected, output)
		}
	})

	t.Run("kubectl error", func(t *testing.T) {
		ctx := newCtx()
		ctx.DryRun = true
		config := ankh.AnkhFile{
			Namespace: NAMESPACE,
		}

		_, err := Execute(ctx, Apply, INPUT, config, execCommandContextFail)

		expected := "error running the kubectl command:\nOoops!"

		if err == nil || err.Error() != expected {
			t.Errorf("Expected output: %s; found: %s", expected, err.Error())
		}
	})
}
