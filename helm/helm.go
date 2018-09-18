package helm

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
)

func explain(args []string) string {
	indent := "    "

	explain := strings.Join(args[0:len(args)-1], " ")

	// The final arg is on its own line (this is typically the Chart argument)
	explain += fmt.Sprintf(" \\\n%v%v", indent, args[len(args)-1])

	// Newline and 2 space indent on each -f arg
	explain = strings.Replace(explain, "-f", fmt.Sprintf("\\\n%v-f", indent), -1)

	// This is going to be followed by 0 or more other explains, so chain them with &&.
	// The caller strips the tailing && and whitespace. Weird, but fine.
	return explain + " && \\\n"
}

func findChartFilesImpl(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, chart ankh.Chart) (ankh.ChartFiles, error) {
	name := chart.Name
	version := chart.Version

	dirPath := chart.Path
	if dirPath == "" {
		// No explicit path set, so try the "charts" subdirectory.
		dirPath = filepath.Join(filepath.Dir(ankhFile.Path), "charts", name)
	}
	ctx.Logger.Debugf("Using directory %v for chart %v", dirPath, name)
	_, dirErr := os.Stat(dirPath)

	files := ankh.ChartFiles{}
	// Setup a directory where we'll either copy the chart files, if we've got a
	// directory, or we'll download and extract a tarball to the temp dir. Then
	// we'll mutate some of the ankh specific files based on the current
	// environment and resource profile. Then we'll use those files as arguments
	// to the helm command.
	tmpDir, err := ioutil.TempDir(ctx.DataDir, name+"-")
	if err != nil {
		return files, err
	}

	tarballFileName := fmt.Sprintf("%s-%s.tgz", name, version)
	tarballURL := fmt.Sprintf("%s/%s", strings.TrimRight(
		ctx.AnkhConfig.CurrentContext.HelmRegistryURL, "/"), tarballFileName)

	// If we already have a dir, let's just copy it to a temp directory so we can
	// make changes to the ankh specific yaml files before passing them as `-f`
	// args to `helm template`
	if dirErr == nil {
		if err := util.CopyDir(dirPath, filepath.Join(tmpDir, name)); err != nil {
			return files, err
		}
	} else {
		ok := false
		for attempt := 1; attempt <= 5; attempt++ {
			ctx.Logger.Debugf("downloading chart from %s (attempt %v)", tarballURL, attempt)
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
			client := &http.Client{
				Transport: tr,
				Timeout:   time.Duration(5 * time.Second),
			}
			resp, err := client.Get(tarballURL)
			if err != nil {
				ctx.Logger.Warningf("got an error %v when trying to call %v (attempt %v)",
					err, tarballURL, attempt)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode == 200 {
				ctx.Logger.Debugf("untarring chart to %s", tmpDir)
				if err = util.Untar(tmpDir, resp.Body); err != nil {
					return files, err
				}

				ok = true
				break
			} else {
				ctx.Logger.Warningf("got a status code %v when trying to call %s (attempt %v)", resp.StatusCode, tarballURL, attempt)
			}
		}
		if !ok {
			return files, fmt.Errorf("failed to fetch helm chart from URL: %v", tarballURL)
		}
	}

	chartDir := filepath.Join(tmpDir, name)
	files = ankh.ChartFiles{
		Dir:                      tmpDir,
		ChartDir:                 chartDir,
		GlobalPath:               filepath.Join(tmpDir, "global.yaml"),
		ValuesPath:               filepath.Join(chartDir, "values.yaml"),
		AnkhValuesPath:           filepath.Join(chartDir, "ankh-values.yaml"),
		AnkhResourceProfilesPath: filepath.Join(chartDir, "ankh-resource-profiles.yaml"),
		AnkhReleasesPath:         filepath.Join(chartDir, "ankh-releases.yaml"),
	}

	return files, nil
}

var findChartFiles = findChartFilesImpl
var execContext = exec.Command

func templateChart(ctx *ankh.ExecutionContext, chart ankh.Chart, ankhFile ankh.AnkhFile) (string, error) {
	currentContext := ctx.AnkhConfig.CurrentContext
	helmArgs := []string{"helm", "template"}

	if ankhFile.Namespace != "" {
		helmArgs = append(helmArgs, []string{"--namespace", ankhFile.Namespace}...)
	}

	if currentContext.Release != "" {
		helmArgs = append(helmArgs, []string{"--name", currentContext.Release}...)
	}

	for key, val := range ctx.HelmSetValues {
		helmArgs = append(helmArgs, "--set", key+"="+val)
	}

	files, err := findChartFiles(ctx, ankhFile, chart)

	if err != nil {
		return "", err
	}

	// Load `values` from chart
	_, valuesErr := os.Stat(files.AnkhValuesPath)
	if valuesErr == nil {
		if _, err := util.CreateReducedYAMLFile(files.AnkhValuesPath, currentContext.EnvironmentClass); err != nil {
			return "", fmt.Errorf("unable to process ankh-values.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", files.AnkhValuesPath)
	}

	// Load `resource-profiles` from chart
	_, resourceProfilesError := os.Stat(files.AnkhResourceProfilesPath)
	if resourceProfilesError == nil {
		if _, err := util.CreateReducedYAMLFile(files.AnkhResourceProfilesPath, currentContext.ResourceProfile); err != nil {
			return "", fmt.Errorf("unable to process ankh-resource-profiles.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", files.AnkhResourceProfilesPath)
	}

	// Load `releases` from chart
	_, releasesError := os.Stat(files.AnkhReleasesPath)
	if releasesError == nil {
		if _, err := util.CreateReducedYAMLFile(files.AnkhReleasesPath, currentContext.Release); err != nil {
			return "", fmt.Errorf("unable to process ankh-releases.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", files.AnkhReleasesPath)
	}

	// Load `default-values` from ankhFile
	if chart.DefaultValues != nil {
		defaultValuesPath := filepath.Join(files.Dir, "default-values.yaml")
		defaultValuesBytes, err := yaml.Marshal(chart.DefaultValues)
		if err != nil {
			return "", err
		}

		if err := ioutil.WriteFile(defaultValuesPath, defaultValuesBytes, 0644); err != nil {
			return "", err
		}

		helmArgs = append(helmArgs, "-f", defaultValuesPath)
	}

	// Load `values` from ankhFile
	if chart.Values != nil && chart.Values[currentContext.EnvironmentClass] != nil {
		valuesPath := filepath.Join(files.Dir, "values.yaml")
		valuesBytes, err := yaml.Marshal(chart.Values[currentContext.EnvironmentClass])
		if err != nil {
			return "", err
		}

		if err := ioutil.WriteFile(valuesPath, valuesBytes, 0644); err != nil {
			return "", err
		}

		helmArgs = append(helmArgs, "-f", valuesPath)
	}

	// Load `resource-profiles` from ankhFile
	if chart.ResourceProfiles != nil && chart.ResourceProfiles[currentContext.ResourceProfile] != nil {
		resourceProfilesPath := filepath.Join(files.Dir, "resource-profiles.yaml")
		resourceProfilesBytes, err := yaml.Marshal(chart.ResourceProfiles[currentContext.ResourceProfile])

		if err != nil {
			return "", err
		}

		if err := ioutil.WriteFile(resourceProfilesPath, resourceProfilesBytes, 0644); err != nil {
			return "", err
		}

		helmArgs = append(helmArgs, "-f", resourceProfilesPath)
	}

	// Load `releases` from ankhFile
	if chart.Releases != nil && chart.Releases[currentContext.Release] != nil {
		releasesPath := filepath.Join(files.Dir, "releases.yaml")
		releasesBytes, err := yaml.Marshal(chart.Releases[currentContext.Release])

		if err != nil {
			return "", err
		}

		if err := ioutil.WriteFile(releasesPath, releasesBytes, 0644); err != nil {
			return "", err
		}

		helmArgs = append(helmArgs, "-f", releasesPath)
	}

	// Check if Global contains anything and append them
	if currentContext.Global != nil {
		ctx.Logger.Debugf("found global values for the current context")

		globalYamlBytes, err := yaml.Marshal(map[string]interface{}{
			"global": currentContext.Global,
		})
		if err != nil {
			return "", err
		}

		ctx.Logger.Debugf("writing global values to %s", files.GlobalPath)

		if err := ioutil.WriteFile(files.GlobalPath, globalYamlBytes, 0644); err != nil {
			return "", err
		}

		helmArgs = append(helmArgs, "-f", files.GlobalPath)
	}

	helmArgs = append(helmArgs, files.ChartDir)

	ctx.Logger.Debugf("running helm command %s", strings.Join(helmArgs, " "))

	helmCmd := execContext(helmArgs[0], helmArgs[1:]...)

	if ctx.Mode == ankh.Explain {
		return explain(helmCmd.Args), nil
	}
	var stdout, stderr bytes.Buffer
	helmCmd.Stdout = &stdout
	helmCmd.Stderr = &stderr

	err = helmCmd.Run()
	var helmOutput, helmError = string(stdout.Bytes()), string(stderr.Bytes())
	if err != nil {
		outputMsg := ""
		if len(helmError) > 0 {
			outputMsg = fmt.Sprintf(" -- the helm process had the following output on stderr:\n%s", helmError)
		}
		return "", fmt.Errorf("error running the helm command: %v%v", err, outputMsg)
	}

	return string(helmOutput), nil
}

func Version() (string, error) {
	helmArgs := []string{"helm", "version", "--client"}
	helmCmd := exec.Command(helmArgs[0], helmArgs[1:]...)
	helmOutput, err := helmCmd.CombinedOutput()
	if err != nil {
		outputMsg := ""
		if len(helmOutput) > 0 {
			outputMsg = fmt.Sprintf(" -- the helm process had the following output on stdout/stderr:\n%s", helmOutput)
		}
		return "", fmt.Errorf("%v%v", err, outputMsg)
	}
	return string(helmOutput), nil
}

func Template(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile) (string, error) {
	finalOutput := ""

	if len(ankhFile.Charts) > 0 {
		ctx.Logger.Debugf("templating charts")

		for _, chart := range ankhFile.Charts {
			extraString := ""
			if chart.Version != "" {
				extraString = fmt.Sprintf(" using version \"%v\"", chart.Version)
			} else if chart.Path != "" {
				extraString = fmt.Sprintf(" using path \"%v\"", chart.Path)
			}
			ctx.Logger.Infof("Templating chart \"%s\"%s", chart.Name, extraString)
			chartOutput, err := templateChart(ctx, chart, ankhFile)
			if err != nil {
				return finalOutput, err
			}
			finalOutput += chartOutput
		}
	} else {
		ctx.Logger.Infof(
			"%s does not contain any charts. Template commands only operate on ankh.yaml files containing charts",
			ctx.AnkhFilePath)
	}
	return finalOutput, nil
}
