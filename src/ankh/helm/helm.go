package helm

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ankh"
	"ankh/util"

	"gopkg.in/yaml.v2"
)

func templateChart(ctx *ankh.ExecutionContext, chart ankh.Chart, ankhFile ankh.AnkhFile) (string, error) {
	currentContext := ctx.AnkhConfig.CurrentContext
	helmArgs := []string{"helm", "template", "--kube-context", currentContext.KubeContext, "--namespace", ankhFile.Namespace}

	if currentContext.Release != "" {
		helmArgs = append(helmArgs, []string{"--name", currentContext.Release}...)
	}

	// Check if Global contains anything and append `--set` flags to the helm
	// command for each item
	if currentContext.Global != nil {
		for _, item := range util.Collapse(currentContext.Global, nil, nil) {
			k := strings.Split(item, "=")
			if _, inMap := ctx.HelmSetValues[k[0]]; inMap {
				ctx.Logger.Debugf("Overriding ankh config global value %v with value supplied to command line", k[0])
			} else {
				helmArgs = append(helmArgs, "--set", "global."+item)
			}
		}
	}

	for key, val := range ctx.HelmSetValues {
		helmArgs = append(helmArgs, "--set", "global."+key+"="+val)
	}
	files, err := ankh.FindChartFiles(ctx, ankhFile, chart)

	if err != nil {
		return "", err
	}

	// Load `values` from chart
	_, valuesErr := os.Stat(files.AnkhValuesPath)
	if valuesErr == nil {
		if _, err := ankh.CreateReducedYAMLFile(files.AnkhValuesPath, currentContext.Environment); err != nil {
			return "", fmt.Errorf("unable to process ankh-values.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", files.AnkhValuesPath)
	}

	// Load `profiles` from chart
	_, resourceProfilesError := os.Stat(files.AnkhResourceProfilesPath)
	if resourceProfilesError == nil {
		if _, err := ankh.CreateReducedYAMLFile(files.AnkhResourceProfilesPath, currentContext.ResourceProfile); err != nil {
			return "", fmt.Errorf("unable to process ankh-resource-profiles.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", files.AnkhResourceProfilesPath)
	}

	// TODO: add validation for secrets
	if chart.Secrets != nil {
		secretsPath := filepath.Join(files.Dir, "secrets.yaml")
		secretsBytes, err := yaml.Marshal(chart.Secrets[currentContext.Environment])
		if err != nil {
			return "", err
		}

		if err := ioutil.WriteFile(secretsPath, secretsBytes, 0644); err != nil {
			return "", err
		}

		helmArgs = append(helmArgs, "-f", secretsPath)
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
	if chart.Values != nil && chart.Values[currentContext.Environment] != nil {
		valuesPath := filepath.Join(files.Dir, "values.yaml")
		valuesBytes, err := yaml.Marshal(chart.Values[currentContext.Environment])
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

	helmArgs = append(helmArgs, files.ChartDir)

	ctx.Logger.Debugf("running helm command %s", strings.Join(helmArgs, " "))

	helmCmd := exec.Command(helmArgs[0], helmArgs[1:]...)
	helmOutput, err := helmCmd.CombinedOutput()

	if err != nil {
		return "", fmt.Errorf("error running the helm command: %s", helmOutput)
	}

	return string(helmOutput), nil
}

func Template(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile) (string, error) {
	finalOutput := ""

	if len(ankhFile.Charts) > 0 {
		ctx.Logger.Debugf("templating charts")

		if ctx.Chart == "" {
			for _, chart := range ankhFile.Charts {
				ctx.Logger.Debugf("templating chart '%s'", chart.Name)
				chartOutput, err := templateChart(ctx, chart, ankhFile)
				if err != nil {
					return finalOutput, err
				}
				finalOutput += chartOutput
			}
		} else {
			for _, chart := range ankhFile.Charts {
				if chart.Name == ctx.Chart {
					ctx.Logger.Debugf("templating chart '%s'", chart.Name)

					chartOutput, err := templateChart(ctx, chart, ankhFile)
					if err != nil {
						return finalOutput, err
					}
					return chartOutput, nil
				}
			}
			ctx.Logger.Fatalf("Chart %s was specified with `--chart` but does not exist in the charts array", ctx.Chart)
		}
	} else {
		ctx.Logger.Infof(
			"%s does not contain any charts. Template commands only operate on ankh.yaml files containing charts",
			ctx.AnkhFilePath)
	}
	return finalOutput, nil
}
