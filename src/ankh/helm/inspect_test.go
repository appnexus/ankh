package helm

import (
	"ankh"
	"github.com/sirupsen/logrus"
	"testing"
	"ankh/util"
	"os"
	"os/exec"
	"fmt"
	"strings"
	"io/ioutil"
)

const CHART_NAME = "test_chart"
const ANKH_FILE_PATH = "/path/to/ankh.yaml"
const TEST_DIR = "/tmp/ankh"
const ANKH_VALUES = "ankh-values.yaml"
const RESOURCE_PROFILES = "ankh-resource-profiles.yaml"
const VALUES = "values.yaml"

func newCtx() *ankh.ExecutionContext {
	return &ankh.ExecutionContext{
		Logger: logrus.New(),
		AnkhFilePath:ANKH_FILE_PATH,
		AnkhConfig: ankh.AnkhConfig{
			CurrentContext: ankh.Context{
				Environment:     "dev",
				ResourceProfile: "constrained",
				HelmRegistryURL: "http://localhost",
				KubeContext:     "dev",
			},
			SupportedEnvironments:     []string{"dev"},
			SupportedResourceProfiles: []string{"constrained"},
		},
	}
}

func newChart() ankh.Chart {
	return ankh.Chart{
		Name: CHART_NAME,
		DefaultValues: map[string]interface{}{
			"default_key": "default_value",
		},
		Values: map[string]interface{}{
			"prod": map[string]interface{}{
				"host": "test.adnxs.net",
			},
			"dev": map[string]interface{}{
				"host": "test.devnxs.net"},
		},
		ResourceProfiles: map[string]interface{}{
			"natural": map[string]interface{}{
				"cpu": 0.3},
			"constrained": map[string]interface{}{
				"cpu": 0.1},
		},
	}
}

func newAnkhFile() ankh.AnkhFile {
	return ankh.AnkhFile{}
}

func findFilesMock(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, chart ankh.Chart) (ankh.ChartFiles, error) {
	return ankh.ChartFiles{
		AnkhValuesPath: TEST_DIR + "/" + ANKH_VALUES,
		AnkhResourceProfilesPath: TEST_DIR + "/" + RESOURCE_PROFILES,
		ValuesPath:TEST_DIR + "/" + VALUES,
		ChartDir:TEST_DIR,
	}, nil
}

func TestInspectValues(t *testing.T) {
	t.Run("using context", func(t *testing.T) {
		// overriding functions
		oldFindFiles := findChartFiles
		findChartFiles = findFilesMock

		defer func() {
			findChartFiles = oldFindFiles
		}()

		ctx := newCtx()
		ctx.UseContext = true
		chart := newChart()
		ankhFile := newAnkhFile()
		err := util.CopyDir("testdata/", TEST_DIR)
		defer os.RemoveAll(TEST_DIR)

		if err != nil {
			t.Error(err.Error())
		}

		out, err := InspectValues(ctx, ankhFile, chart)
		if err != nil {
			t.Error(err.Error())
		}

		expected := `---
# Chart: test_chart
# Source: /path/to/ankh.yaml
default_values:
  default_key: default_value
values:
  host: test.devnxs.net
resource_profiles:
  cpu: 0.1
---
# Source: /tmp/ankh/ankh-resource-profiles.yaml
replicas: 1
---
# Source: /tmp/ankh/ankh-values.yaml
port: 8080
---
# Source: /tmp/ankh/values.yaml
host: localhost
port: 8080
name: test-app
`
  	if out != expected {
  		t.Errorf("\nExpected output: %s\n\n Found: %s", expected, out)
		}
	})

	t.Run("ankh file, no context", func(t *testing.T) {
		// overriding functions
		oldFindFiles := findChartFiles
		findChartFiles = findFilesMock

		defer func() {
			findChartFiles = oldFindFiles
		}()

		ctx := newCtx()
		ctx.UseContext = false
		chart := newChart()
		ankhFile := newAnkhFile()
		err := util.CopyDir("testdata/", TEST_DIR)
		defer os.RemoveAll(TEST_DIR)

		if err != nil {
			t.Error(err.Error())
		}

		out, err := InspectValues(ctx, ankhFile, chart)
		if err != nil {
			t.Error(err.Error())
		}

		expected := `---
# Chart: test_chart
# Source: /path/to/ankh.yaml
default_values:
  default_key: default_value
values:
  dev:
    host: test.devnxs.net
  prod:
    host: test.adnxs.net
resource_profiles:
  constrained:
    cpu: 0.1
  natural:
    cpu: 0.3
---
# Source: /tmp/ankh/ankh-resource-profiles.yaml
constrained:
  replicas: 1
natural:
  replicas: 2
---
# Source: /tmp/ankh/ankh-values.yaml
production:
  port: 80
dev:
  port: 8080
---
# Source: /tmp/ankh/values.yaml
host: localhost
port: 8080
name: test-app
`
		if out != expected {
			t.Errorf("\nExpected output: %s\n\n Found: %s", expected, out)
		}
	})
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

	// Mocked exec.Command will return input command
	output := strings.Join(os.Args[3:], " ")

	fmt.Fprintf(os.Stdout, output)
	os.Exit(0)
}

func TestInspectChart(t *testing.T) {
	t.Run("inspect chart", func(t *testing.T) {
		// overriding functions
		oldExec := execContext
		execContext = execCommandContext

		oldFindFiles := findChartFiles
		findChartFiles = findFilesMock

		defer func() {
			execContext = oldExec
			findChartFiles = oldFindFiles
		}()

		ctx := newCtx()
		chart := newChart()
		ankhFile := newAnkhFile()

		out, err := InspectChart(ctx, ankhFile, chart)
		if err != nil {
			t.Error(err.Error())
		}

		out = strings.TrimSpace(out)

		expected := `# Chart: test_chart
helm inspect chart --kube-context dev`

		if out != expected {
			t.Errorf("Expected: %s, found: %s", expected, out)
		}
	})
}

func TestInspectTemplates(t *testing.T) {
	t.Run("inspect templates", func(t *testing.T) {
		// overriding functions
		oldFindFiles := findChartFiles
		findChartFiles = findFilesMock

		defer func() {
			findChartFiles = oldFindFiles
		}()

		err := util.CopyDir("testdata/", TEST_DIR)
		defer os.RemoveAll(TEST_DIR)

		if err != nil {
			t.Error(err.Error())
		}

		ctx := newCtx()
		chart := newChart()
		ankhFile := newAnkhFile()

		out, err := InspectTemplates(ctx, ankhFile, chart)
		if err != nil {
			t.Error(err.Error())
		}

		out = strings.TrimSpace(out)
		bytes, _ := ioutil.ReadFile("testdata/inspect-template.yaml")

		expected := strings.TrimSpace(string(bytes))
		if out != string(expected) {
			t.Errorf("Expected: %s, found: %s", expected, out)
		}
	})
}
