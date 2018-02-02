package helm

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ankh"
	"ankh/util"

	"gopkg.in/yaml.v2"
)

type chartFiles struct {
	Dir                      string
	ChartDir                 string
	ValuesPath               string
	AnkhValuesPath           string
	AnkhResourceProfilesPath string
}

func FindChartFiles(ctx *ankh.ExecutionContext, name string, version string, ankhFile ankh.AnkhFile) (chartFiles, error) {
	dirPath := filepath.Join(filepath.Dir(ankhFile.Path), "charts", name)
	_, dirErr := os.Stat(dirPath)

	files := chartFiles{}
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

		// TODO: this code should be modified to properly fetch charts
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
				return files, err
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
	files = chartFiles{
		Dir:                      tmpDir,
		ChartDir:                 chartDir,
		ValuesPath:               filepath.Join(chartDir, "values.yaml"),
		AnkhValuesPath:           filepath.Join(chartDir, "ankh-values.yaml"),
		AnkhResourceProfilesPath: filepath.Join(chartDir, "ankh-resource-profiles.yaml"),
	}

	return files, nil
}

func templateChart(ctx *ankh.ExecutionContext, chart ankh.Chart, ankhFile ankh.AnkhFile) (string, error) {
	currentContext := ctx.AnkhConfig.CurrentContext
	helmArgs := []string{"helm", "template", "--kube-context", currentContext.KubeContext, "--namespace", ankhFile.Namespace}

	if currentContext.Release != "" {
		helmArgs = append(helmArgs, []string{"--release", currentContext.Release}...)
	}

	// Check if Global contains anything and append `--set` flags to the helm
	// command for each item
	if currentContext.Global != nil {
		for _, item := range util.Collapse(currentContext.Global, nil, nil) {
			helmArgs = append(helmArgs, "--set", "global."+item)
		}
	}

	files, err := FindChartFiles(ctx, chart.Name, chart.Version, ankhFile)
	if err != nil {
		return "", err
	}

	// Load `values` from chart
	_, valuesErr := os.Stat(files.AnkhValuesPath)
	if valuesErr == nil {
		if _, err := CreateReducedYAMLFile(files.AnkhValuesPath, currentContext.Environment); err != nil {
			return "", fmt.Errorf("unable to process ankh-values.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", files.AnkhValuesPath)
	}

	// Load `profiles` from chart
	_, profilesError := os.Stat(files.AnkhResourceProfilesPath)
	if profilesError == nil {
		if _, err := CreateReducedYAMLFile(files.AnkhResourceProfilesPath, currentContext.Profile); err != nil {
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

	// Load `default_values` from ankhFile
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

	// Load `resource_profiles` from ankhFile
	if chart.ResourceProfiles != nil && chart.ResourceProfiles[currentContext.Profile] != nil {
		profilesPath := filepath.Join(files.Dir, "resource-profiles.yaml")
		profilesBytes, err := yaml.Marshal(chart.ResourceProfiles[currentContext.Profile])
		if err != nil {
			return "", err
		}

		if err := ioutil.WriteFile(profilesPath, profilesBytes, 0644); err != nil {
			return "", err
		}

		helmArgs = append(helmArgs, "-f", profilesPath)
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

func CreateReducedYAMLFile(filename, key string) ([]byte, error) {
	in := make(map[string]interface{})
	var result []byte
	inBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return result, err
	}

	if err = yaml.Unmarshal(inBytes, &in); err != nil {
		return result, err
	}

	out := make(map[interface{}]interface{})

	if in[key] == nil {
		return result, fmt.Errorf("missing `%s` key", key)
	}

	switch t := in[key].(type) {
	case map[interface{}]interface{}:
		for k, v := range t {
			// TODO: using `.(string)` here could cause a panic in cases where the
			// key isn't a string, which is pretty uncommon

			// TODO: validate
			out[k.(string)] = v
		}
	default:
		out[key] = in[key]
	}

	outBytes, err := yaml.Marshal(&out)
	if err != nil {
		return result, err
	}

	if err := ioutil.WriteFile(filename, outBytes, 0644); err != nil {
		return result, err
	}

	return outBytes, nil
}

func Template(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile) (string, error) {
	finalOutput := ""

	if len(ankhFile.Charts) > 0 {
		ctx.Logger.Debugf("templating charts")
		for _, chart := range ankhFile.Charts {
			ctx.Logger.Debugf("templating chart '%s'", chart.Name)

			chartOutput, err := templateChart(ctx, chart, ankhFile)
			if err != nil {
				return finalOutput, err
			}
			finalOutput += chartOutput
		}
	}

	return finalOutput, nil
}

func getChartFileContent(ctx *ankh.ExecutionContext, path string, useContext bool, key string) ([]byte, error) {
	var result []byte
	bytes, err := ioutil.ReadFile(fmt.Sprintf("%s", path))
	if err == nil {

		if useContext {
			bytes, err = CreateReducedYAMLFile(path, key)
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

func InspectValues(ctx *ankh.ExecutionContext, chart ankh.Chart, ankhFile ankh.AnkhFile) (string, error) {
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
			ResourceProfiles: chart.ResourceProfiles[ctx.AnkhConfig.CurrentContext.Profile],
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
		return "", nil
	}
	result += string(out)

	files, err := FindChartFiles(ctx, chart.Name, chart.Version, ankhFile)
	if err != nil {
		return "", nil
	}

	bytes, err := getChartFileContent(ctx, files.AnkhResourceProfilesPath, ctx.UseContext, ctx.AnkhConfig.CurrentContext.Profile)
	if err != nil {
		return "", nil
	}
	if len(bytes) > 0 {
		result += string(bytes)
	}

	bytes, err = getChartFileContent(ctx, files.AnkhValuesPath, ctx.UseContext, ctx.AnkhConfig.CurrentContext.Environment)
	if err != nil {
		return "", nil
	}
	if len(bytes) > 0 {
		result += string(bytes)
	}

	bytes, err = getChartFileContent(ctx, files.ValuesPath, false, "")
	if err != nil {
		return "", nil
	}
	if len(bytes) > 0 {
		result += string(bytes)
	}

	return result, nil
}

func InspectChart(ctx *ankh.ExecutionContext, chart ankh.Chart, ankhFile ankh.AnkhFile) (string, error) {
	var result string

	ctx.Logger.Debug("Inspecting chart.yaml for chart %s", chart.Name)

	currentContext := ctx.AnkhConfig.CurrentContext
	result += fmt.Sprintf("# Chart: %s\n", chart.Name)
	files, err := FindChartFiles(ctx, chart.Name, chart.Version, ankhFile)
	if err != nil {
		return "", err
	}
	helmArgs := []string{"helm", "inspect", "chart", "--kube-context", currentContext.KubeContext}

	helmArgs = append(helmArgs, files.ChartDir)

	ctx.Logger.Debugf("running helm command %s", strings.Join(helmArgs, " "))

	helmCmd := exec.Command(helmArgs[0], helmArgs[1:]...)
	helmOutput, err := helmCmd.CombinedOutput()

	if err != nil {
		return "", fmt.Errorf("error running the helm command: %s", helmOutput)
	}

	result += string(helmOutput)

	return result, nil
}

func InspectTemplates(ctx *ankh.ExecutionContext, chart ankh.Chart, ankhFile ankh.AnkhFile) (string, error) {
	var result string

	ctx.Logger.Debug("Inspecting templates for chart %s", chart.Name)
	files, err := FindChartFiles(ctx, chart.Name, chart.Version, ankhFile)
	if err != nil {
		return "", err
	}

	dir := files.ChartDir + "/templates/"

	var templates []os.FileInfo
	templates, err = ioutil.ReadDir(dir)
	if err != nil {
		return "", err
	}

	result += "---\n# Chart: " + chart.Name
	for _, template := range templates {
		result += fmt.Sprintf("\n# Source: %s/templates/%s\n", chart.Name, template.Name())
		path := dir + template.Name()
		bytes, err := ioutil.ReadFile(path)
		if err != nil {
			return "", err
		}

		result += string(bytes)
	}

	return result, nil
}
