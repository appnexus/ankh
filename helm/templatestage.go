package helm

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
	"github.com/appnexus/ankh/util"
)

type TemplateStage struct {
	charts []ankh.Chart
}

func NewTemplateStage(charts []ankh.Chart) plan.Stage {
	return TemplateStage{charts: charts}
}

func (stage TemplateStage) Execute(ctx *ankh.ExecutionContext, input *string, namespace string, wildCardLabels []string) (string, error) {
	// Template, then filter.
	helmOutput, err := helmTemplate(ctx, stage.charts, namespace)
	if err != nil {
		return "", err
	}

	if len(ctx.Filters) > 0 {
		ctx.Logger.Debugf("Filtering with inclusive list `%v`", ctx.Filters)
		helmOutput = filterOutput(ctx.Filters, helmOutput)
	}
	return helmOutput, nil
}

func getDirectoryFile(ctx *ankh.ExecutionContext, chart ankh.Chart, files ankh.ChartFiles, kind string, match string) string {
	// This should be determined elsewhere.
	directory := chart.ChartMeta.ConfigMeta.Paths[kind]
	if directory == "" {
		directory = fmt.Sprintf("ankh/%v", kind)
	}

	path := filepath.Join(files.ChartDir, directory, fmt.Sprintf("%v.yaml", match))
	ctx.Logger.Debugf("* Checking for %v file %v", kind, path)
	_, err := os.Stat(path)
	if err != nil {
		ctx.Logger.Debugf("-- not found, skipping...")
		return ""
	}

	ctx.Logger.Debugf("-- Found %v file %v", kind, path)
	return path

}

func getValuesFromChartFiles(ctx *ankh.ExecutionContext, chart ankh.Chart, files ankh.ChartFiles) ([]string, error) {
	currentContext := ctx.AnkhConfig.CurrentContext
	helmArgs := []string{}

	useDirectory := chart.ChartMeta.ConfigMeta.Type == "directory"

	// Load `values` from ankh-values.yaml
	if useDirectory {
		path := getDirectoryFile(ctx, chart, files, "values", currentContext.EnvironmentClass)
		if path != "" {
			helmArgs = append(helmArgs, "-f", path)
		}
	} else {
		ctx.Logger.Debugf("Checking for environment-class file %v", files.AnkhValuesPath)
		_, valuesErr := os.Stat(files.AnkhValuesPath)
		if valuesErr == nil {
			if _, err := util.CreateReducedYAMLFile(files.AnkhValuesPath, currentContext.EnvironmentClass, true); err != nil {
				return []string{}, fmt.Errorf("unable to process ankh-values.yaml file for chart '%s': %v", chart.Name, err)
			}
			helmArgs = append(helmArgs, "-f", files.AnkhValuesPath)
		}
	}

	// Load `resource-profiles` from ankh-resource-profiles.yaml
	if useDirectory {
		path := getDirectoryFile(ctx, chart, files, "resource-profiles", currentContext.ResourceProfile)
		if path != "" {
			helmArgs = append(helmArgs, "-f", path)
		}
	} else {
		_, resourceProfilesError := os.Stat(files.AnkhResourceProfilesPath)
		if resourceProfilesError == nil {
			if _, err := util.CreateReducedYAMLFile(files.AnkhResourceProfilesPath, currentContext.ResourceProfile, true); err != nil {
				return []string{}, fmt.Errorf("unable to process ankh-resource-profiles.yaml file for chart '%s': %v", chart.Name, err)
			}
			helmArgs = append(helmArgs, "-f", files.AnkhResourceProfilesPath)
		}
	}

	// Load `releases` from ankh-releases.yaml
	if currentContext.Release != "" {
		if useDirectory {
			path := getDirectoryFile(ctx, chart, files, "releases", currentContext.Release)
			if path != "" {
				helmArgs = append(helmArgs, "-f", path)
			}
		} else {
			_, releasesError := os.Stat(files.AnkhReleasesPath)
			if releasesError == nil {
				out, err := util.CreateReducedYAMLFile(files.AnkhReleasesPath, currentContext.Release, false)
				if err != nil {
					return []string{}, fmt.Errorf("unable to process ankh-releases.yaml file for chart '%s': %v", chart.Name, err)
				}
				if len(out) > 0 {
					helmArgs = append(helmArgs, "-f", files.AnkhReleasesPath)
				}
			}
		}
	}

	return helmArgs, nil
}

func getValuesFromChartObject(currentContext ankh.Context, chart ankh.Chart, outputDir string) ([]string, error) {
	helmArgs := []string{}

	// Load `default-values`
	if chart.DefaultValues != nil {
		defaultValuesPath := filepath.Join(outputDir, "default-values.yaml")
		defaultValuesBytes, err := yaml.Marshal(chart.DefaultValues)
		if err != nil {
			return []string{}, err
		}

		if err := ioutil.WriteFile(defaultValuesPath, defaultValuesBytes, 0644); err != nil {
			return []string{}, err
		}

		helmArgs = append(helmArgs, "-f", defaultValuesPath)
	}

	// Load `values`
	if chart.Values != nil {
		values, err := util.MapSliceRegexMatch(chart.Values, currentContext.EnvironmentClass)
		if err != nil {
			return []string{}, fmt.Errorf("Failed to load `values` for chart %v: %v", chart.Name, err)
		}
		if values != nil {
			valuesPath := filepath.Join(outputDir, "values.yaml")
			valuesBytes, err := yaml.Marshal(values)
			if err != nil {
				return []string{}, err
			}

			if err := ioutil.WriteFile(valuesPath, valuesBytes, 0644); err != nil {
				return []string{}, err
			}

			helmArgs = append(helmArgs, "-f", valuesPath)
		}
	}

	// Load `resource-profiles`
	if chart.ResourceProfiles != nil {
		values, err := util.MapSliceRegexMatch(chart.ResourceProfiles, currentContext.ResourceProfile)
		if err != nil {
			return []string{}, fmt.Errorf("Failed to load `resource-profiles` for chart %v: %v", chart.Name, err)
		}
		if values != nil {
			resourceProfilesPath := filepath.Join(outputDir, "resource-profiles.yaml")
			resourceProfilesBytes, err := yaml.Marshal(values)

			if err != nil {
				return []string{}, err
			}

			if err := ioutil.WriteFile(resourceProfilesPath, resourceProfilesBytes, 0644); err != nil {
				return []string{}, err
			}

			helmArgs = append(helmArgs, "-f", resourceProfilesPath)
		}
	}

	// Load `releases`
	if chart.Releases != nil {
		values, err := util.MapSliceRegexMatch(chart.Releases, currentContext.Release)
		if err != nil {
			return []string{}, fmt.Errorf("Failed to load `releases` for chart %v: %v", chart.Name, err)
		}
		if values != nil {
			releasesPath := filepath.Join(outputDir, "releases.yaml")
			releasesBytes, err := yaml.Marshal(values)

			if err != nil {
				return []string{}, err
			}

			if err := ioutil.WriteFile(releasesPath, releasesBytes, 0644); err != nil {
				return []string{}, err
			}

			helmArgs = append(helmArgs, "-f", releasesPath)
		}
	}

	return helmArgs, nil
}

func getValuesFromGlobal(currentContext ankh.Context, files ankh.ChartFiles) ([]string, error) {
	helmArgs := []string{}

	// Check if Global exists on the current context
	if currentContext.Global != nil {
		globalYamlBytes, err := yaml.Marshal(map[string]interface{}{
			"global": currentContext.Global,
		})
		if err != nil {
			return []string{}, err
		}

		if err := ioutil.WriteFile(files.GlobalPath, globalYamlBytes, 0644); err != nil {
			return []string{}, err
		}

		helmArgs = append(helmArgs, "-f", files.GlobalPath)
	}

	return helmArgs, nil
}

func templateChart(ctx *ankh.ExecutionContext, chart ankh.Chart, namespace string) (string, error) {
	currentContext := ctx.AnkhConfig.CurrentContext
	helmArgs := []string{ctx.AnkhConfig.Helm.Command, "template"}

	if namespace != "" {
		helmArgs = append(helmArgs, []string{"--namespace", namespace}...)
	}

	if currentContext.Release != "" {
		// Helm 2 used `--name` to set release name. Starting in Helm 3, this is a _positional_ argument.
		// TODO: Remove HelmV2 logic when support fully dropped
		if ctx.HelmV2 {
			helmArgs = append(helmArgs, []string{"--name", currentContext.Release}...)
		} else {
			helmArgs = append(helmArgs, []string{currentContext.Release}...)
		}
	}

	for key, val := range ctx.HelmSetValues {
		helmArgs = append(helmArgs, "--set", key+"="+val)
	}

	// Set tagKey=tagValue, if configured and present
	if chart.ChartMeta.TagKey != "" && chart.Tag != nil {
		ctx.Logger.Debugf("Setting helm value %v=%v since chart.ChartMeta.TagKey and chart.Tag are set",
			chart.ChartMeta.TagKey, *chart.Tag)
		helmArgs = append(helmArgs, "--set", chart.ChartMeta.TagKey+"="+*chart.Tag)
	}

	repository := ctx.DetermineHelmRepository(&chart.HelmRepository)
	files, err := findChartFiles(ctx, repository, chart)

	if err != nil {
		return "", err
	}

	// Chart files first...
	chartFileArgs, err := getValuesFromChartFiles(ctx, chart, files)
	if err != nil {
		return "", err
	}
	helmArgs = append(helmArgs, chartFileArgs...)

	// ...and then chart object. Values from the chart object take precedence.
	chartObjectArgs, err := getValuesFromChartObject(currentContext, chart, files.TmpDir)
	if err != nil {
		return "", err
	}
	helmArgs = append(helmArgs, chartObjectArgs...)

	// ...and finally from global sources. These have the highest precedence.
	globalArgs, err := getValuesFromGlobal(currentContext, files)
	if err != nil {
		return "", err
	}
	helmArgs = append(helmArgs, globalArgs...)

	// Construct the final helm command and run it
	helmArgs = append(helmArgs, files.ChartDir)

	ctx.Logger.Debugf("running helm command: '%s'", strings.Join(helmArgs, " "))
	helmCmd := execContext(helmArgs[0], helmArgs[1:]...)

	if ctx.Mode == ankh.Explain {
		out := explain(helmCmd.Args)

		// Need to strip off the final bit of the 'and chain'. Weird, but fine.
		out = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(out), "&& \\"))
		return out, nil
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


func helmTemplate(ctx *ankh.ExecutionContext, charts []ankh.Chart, namespace string) (string, error) {
	finalOutput := ""
	if len(charts) > 0 {
		for _, chart := range charts {
			extraString := ""
			if chart.Version != "" {
				extraString = fmt.Sprintf(" at version \"%v\"", chart.Version)
			} else if chart.Path != "" {
				extraString = fmt.Sprintf(" from path \"%v\"", chart.Path)
			}
			ctx.Logger.Infof("Templating chart \"%s\"%s", chart.Name, extraString)
			chartOutput, err := templateChart(ctx, chart, namespace)
			if err != nil {
				return finalOutput, err
			}
			finalOutput += chartOutput
		}
		if namespace != "" {
			ctx.Logger.Infof("Finished templating charts for namespace %v", namespace)
		} else {
			ctx.Logger.Info("Finished templating charts with an explicit empty namespace")
		}
	} else {
		ctx.Logger.Info("Does not contain any charts. Nothing to do.")
	}
	return finalOutput, nil
}

