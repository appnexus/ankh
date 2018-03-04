package helm

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
	"strings"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
)

func getChartFileContent(ctx *ankh.ExecutionContext, path string, useContext bool, key string) ([]byte, error) {
	var result []byte
	bytes, err := ioutil.ReadFile(fmt.Sprintf("%s", path))
	if err == nil {
		if useContext {
			bytes, err = util.CreateReducedYAMLFile(path, key)
			if err != nil {
				return result, err
			}
		}

		result = bytes
	} else {
		ctx.Logger.Debugf("%s not found", path)
	}

	if len(bytes) > 0 {
		result = append([]byte("---\n# Source: "+path+"\n"), bytes...)
	}

	return result, nil
}

func InspectValues(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, chart ankh.Chart) (string, error) {
	var result string

	ctx.Logger.Debug("Inspecting values for chart %s", chart.Name)

	result += "---\n# Chart: " + chart.Name
	result += fmt.Sprintf("\n# Source: %s\n", ctx.AnkhFilePath)

	type Values struct {
		DefaultValues    map[string]interface{} `yaml:"default_values"`
		Values           interface{}
		ResourceProfiles interface{} `yaml:"resource_profiles"`
	}

	values := Values{}
	if ctx.UseContext {
		values = Values{
			DefaultValues:    chart.DefaultValues,
			Values:           chart.Values[ctx.AnkhConfig.CurrentContext.Environment],
			ResourceProfiles: chart.ResourceProfiles[ctx.AnkhConfig.CurrentContext.ResourceProfile],
		}
	} else {
		values = Values{
			DefaultValues:    chart.DefaultValues,
			Values:           chart.Values,
			ResourceProfiles: chart.ResourceProfiles,
		}
	}

	out, err := yaml.Marshal(values)
	if err != nil {
		return "", err
	}
	result += string(out)

	files, err := findChartFiles(ctx, ankhFile, chart)
	if err != nil {
		return "", err
	}

	bytes, err := getChartFileContent(ctx, files.AnkhResourceProfilesPath, ctx.UseContext, ctx.AnkhConfig.CurrentContext.ResourceProfile)
	if err != nil {
		return "", err
	}
	if len(bytes) > 0 {
		result += string(bytes)
	}

	bytes, err = getChartFileContent(ctx, files.AnkhValuesPath, ctx.UseContext, ctx.AnkhConfig.CurrentContext.Environment)
	if err != nil {
		return "", err
	}
	if len(bytes) > 0 {
		result += string(bytes)
	}

	bytes, err = getChartFileContent(ctx, files.ValuesPath, false, "")
	if err != nil {
		return "", err
	}
	if len(bytes) > 0 {
		result += string(bytes)
	}

	return result, nil
}

func InspectChart(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, chart ankh.Chart) (string, error) {
	var result string

	ctx.Logger.Debugf("Inspecting chart.yaml for chart %s", chart.Name)

	currentContext := ctx.AnkhConfig.CurrentContext
	result += fmt.Sprintf("\n---\n# Chart: %s\n", chart.Name)
	files, err := findChartFiles(ctx, ankhFile, chart)
	if err != nil {
		return "", err
	}
	helmArgs := []string{"helm", "inspect", "chart", "--kube-context", currentContext.KubeContext}

	helmArgs = append(helmArgs, files.ChartDir)

	ctx.Logger.Debugf("running helm command %s", strings.Join(helmArgs, " "))

	helmCmd := execContext(helmArgs[0], helmArgs[1:]...)
	helmOutput, err := helmCmd.CombinedOutput()

	if err != nil {
		return "", fmt.Errorf("error running the helm command: %s", helmOutput)
	}

	result += string(helmOutput)

	return result, nil
}

func InspectTemplates(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, chart ankh.Chart) (string, error) {
	var result string

	ctx.Logger.Debug("Inspecting templates for chart %s", chart.Name)
	files, err := findChartFiles(ctx, ankhFile, chart)
	if err != nil {
		return "", err
	}

	dir := files.ChartDir + "/templates/"

	var templates []os.FileInfo
	templates, err = ioutil.ReadDir(dir)
	if err != nil {
		return "", err
	}

	result += "# Chart: " + chart.Name
	for _, template := range templates {
		result += fmt.Sprintf("\n---\n# Source: %s/templates/%s\n", chart.Name, template.Name())
		path := dir + template.Name()
		bytes, err := ioutil.ReadFile(path)
		if err != nil {
			return "", err
		}

		result += string(bytes)
	}

	return result, nil
}
