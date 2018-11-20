package helm

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
)

const CHART_NAME = "test_chart"
const ANKH_FILE_PATH = "/path/to/ankh.yaml"
const TEST_DIR = "/tmp/ankh"
const ANKH_VALUES = "ankh-values.yaml"
const RESOURCE_PROFILES = "ankh-resource-profiles.yaml"
const RELEASES = "ankh-releases.yaml"
const VALUES = "values.yaml"

func newCtx() *ankh.ExecutionContext {
	return &ankh.ExecutionContext{
		Logger:       logrus.New(),
		AnkhFilePath: ANKH_FILE_PATH,
		AnkhConfig: ankh.AnkhConfig{
			CurrentContext: ankh.Context{
				EnvironmentClass: "dev",
				ResourceProfile:  "constrained",
				Release:          "staging",
				HelmRegistryURL:  "http://localhost",
				KubeContext:      "dev",
			},
		},
	}
}

func newChart() ankh.Chart {
	return ankh.Chart{
		Name: CHART_NAME,
		DefaultValues: map[string]interface{}{
			"default_key": "default_value",
		},
		Values: []yaml.MapItem{
			yaml.MapItem{
				Key:   "production",
				Value: map[string]interface{}{"log_level": 3},
			},
			yaml.MapItem{
				Key:   "dev",
				Value: map[string]interface{}{"log_level": 10},
			},
		},
		ResourceProfiles: []yaml.MapItem{
			yaml.MapItem{
				Key:   "natural",
				Value: map[string]interface{}{"cpu": 0.3},
			},
			yaml.MapItem{
				Key:   "constrained",
				Value: map[string]interface{}{"cpu": 0.1},
			},
		},
		Releases: []yaml.MapItem{
			yaml.MapItem{
				Key:   "production",
				Value: map[string]interface{}{"host": "test.external.com"},
			},
			yaml.MapItem{
				Key:   "staging",
				Value: map[string]interface{}{"host": "test.internal.net"},
			},
		},
	}
}

func newAnkhFile() ankh.AnkhFile {
	return ankh.AnkhFile{}
}

func findFilesMock(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, chart ankh.Chart) (ankh.ChartFiles, error) {
	return ankh.ChartFiles{
		AnkhValuesPath:           TEST_DIR + "/" + ANKH_VALUES,
		AnkhResourceProfilesPath: TEST_DIR + "/" + RESOURCE_PROFILES,
		AnkhReleasesPath:         TEST_DIR + "/" + RELEASES,
		ValuesPath:               TEST_DIR + "/" + VALUES,
		ChartDir:                 TEST_DIR,
	}, nil
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

	// Mocked exec.Command will return input command
	output := strings.Join(os.Args[3:], " ")

	fmt.Fprintf(os.Stdout, output)
	os.Exit(0)
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
		fmt.Println(out)
		out = strings.TrimSpace(out)
		if err != nil {
			t.Error(err.Error())
		}

		bytes, _ := ioutil.ReadFile("testoutput/inspect-values-context.yaml")
		expected := strings.TrimSpace(string(bytes))

		if out != expected {
			t.Errorf(util.LineDiff(expected, out))
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
		out = strings.TrimSpace(out)
		if err != nil {
			t.Error(err.Error())
		}

		bytes, _ := ioutil.ReadFile("testoutput/inspect-values.yaml")
		expected := strings.TrimSpace(string(bytes))

		if out != expected {
			t.Errorf(util.LineDiff(expected, out))
		}
	})
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

		bytes, _ := ioutil.ReadFile("testoutput/inspect-chart.yaml")
		expected := strings.TrimSpace(string(bytes))

		if out != expected {
			t.Errorf(util.LineDiff(expected, out))
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
		bytes, _ := ioutil.ReadFile("testoutput/inspect-template.yaml")
		expected := strings.TrimSpace(string(bytes))
		if out != string(expected) {
			t.Errorf(util.LineDiff(expected, out))
		}
	})
}
