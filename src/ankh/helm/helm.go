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

	"ankh"
	"ankh/util"

	"gopkg.in/yaml.v2"
)

func templateChart(ctx *ankh.ExecutionContext, chart ankh.Chart, ankhFile ankh.AnkhFile, ankhConfig ankh.AnkhConfig) (string, error) {
	currentContext := ankhConfig.CurrentContext
	helmArgs := []string{"helm", "template", "--kube-context", currentContext.KubeContext, "--namespace", ankhFile.Namespace}

	if currentContext.Release != "" {
		helmArgs = append(helmArgs, []string{"--release", currentContext.Release}...)
	}

	dirPath := filepath.Join(filepath.Dir(ankhFile.Path), "charts", chart.Name)
	_, dirErr := os.Stat(dirPath)

	// Setup a directory where we'll either copy the chart files, if we've got a
	// directory, or we'll download and extract a tarball to the temp dir. Then
	// we'll mutate some of the ankh specific files based on the current
	// environment and resource profile. Then we'll use those files as arguments
	// to the helm command.
	tmpDir, err := ioutil.TempDir(ctx.DataDir, chart.Name+"-")
	if err != nil {
		return "", err
	}

	// Check if Global contains anything and append `--set` flags to the helm
	// command for each item
	if currentContext.Global != nil {
		for _, item := range util.Collapse(currentContext.Global, nil, nil) {
			helmArgs = append(helmArgs, "--set", "global."+item)
		}
	}

	tarballFileName := fmt.Sprintf("%s-%s.tgz", chart.Name, chart.Version)
	tarballPath := filepath.Join(filepath.Dir(ankhFile.Path), "charts", tarballFileName)
	tarballURL := fmt.Sprintf("%s/%s", strings.TrimRight(currentContext.HelmRegistryURL, "/"), tarballFileName)

	// If we already have a dir, let's just copy it to a temp directory so we can
	// make changes to the ankh specific yaml files before passing them as `-f`
	// args to `helm template`
	if dirErr == nil {
		if err := util.CopyDir(dirPath, filepath.Join(tmpDir, chart.Name)); err != nil {
			return "", err
		}
	} else {
		ctx.Logger.Debugf("ensuring chart directory is made at %s", filepath.Dir(tarballPath))
		if err := os.MkdirAll(filepath.Dir(tarballPath), 0755); err != nil {
			return "", err
		}

		ctx.Logger.Debugf("opening system file at %s", tarballPath)
		f, err := os.Create(tarballPath)
		if err != nil {
			return "", err
		}
		defer f.Close()

		// TODO: this code should be modified to properly fetch charts
		ctx.Logger.Debugf("downloading chart from %s", tarballURL)
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		client := &http.Client{Transport: tr}
		resp, err := client.Get(tarballURL)
		if err != nil {
			return "", err
		}
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("got a status code %v when trying to call %s", resp.StatusCode, tarballURL)
		}
		// defer resp.Body.Close()

		ctx.Logger.Debugf("untarring chart to %s", tmpDir)
		if err = util.Untar(tmpDir, resp.Body); err != nil {
			return "", err
		}
	}

	chartPath := filepath.Join(tmpDir, chart.Name)
	valuesPath := filepath.Join(chartPath, "ankh-values.yaml")
	profilesPath := filepath.Join(chartPath, "ankh-resource-profiles.yaml")

	// Load `values` from chart
	_, valuesErr := os.Stat(valuesPath)
	if valuesErr == nil {
		if err := createReducedYAMLFile(valuesPath, currentContext.Environment); err != nil {
			return "", fmt.Errorf("unable to process ankh-values.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", valuesPath)
	}

	// Load `profiles` from chart
	_, profilesError := os.Stat(profilesPath)
	if profilesError == nil {
		if err := createReducedYAMLFile(profilesPath, currentContext.Profile); err != nil {
			return "", fmt.Errorf("unable to process ankh-resource-profiles.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", profilesPath)
	}

	// TODO: add validation for secrets
	if chart.Secrets != nil {
		secretsPath := filepath.Join(tmpDir, "secrets.yaml")
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
		defaultValuesPath := filepath.Join(tmpDir, "default-values.yaml")
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
		valuesPath := filepath.Join(tmpDir, "values.yaml")
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
		profilesPath := filepath.Join(tmpDir, "resource-profiles.yaml")
		profilesBytes, err := yaml.Marshal(chart.ResourceProfiles[currentContext.Profile])
		if err != nil {
			return "", err
		}

		if err := ioutil.WriteFile(profilesPath, profilesBytes, 0644); err != nil {
			return "", err
		}

		helmArgs = append(helmArgs, "-f", profilesPath)
	}

	helmArgs = append(helmArgs, chartPath)

	ctx.Logger.Debugf("running helm command %s", strings.Join(helmArgs, " "))

	helmCmd := exec.Command(helmArgs[0], helmArgs[1:]...)
	helmOutput, err := helmCmd.CombinedOutput()

	if err != nil {
		return "", fmt.Errorf("error running the helm command: %s", helmOutput)
	}

	return string(helmOutput), nil
}

func createReducedYAMLFile(filename, key string) error {
	in := make(map[string]interface{})

	inBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}

	if err = yaml.Unmarshal(inBytes, &in); err != nil {
		return err
	}

	out := make(map[interface{}]interface{})

	if in[key] == nil {
		return fmt.Errorf("missing `%s` key", key)
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
		return err
	}

	if err := ioutil.WriteFile(filename, outBytes, 0644); err != nil {
		return err
	}

	return nil
}

func Template(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, ankhConfig ankh.AnkhConfig) (string, error) {
	finalOutput := ""

	ctx.Logger.Infof("beginning templating of %s", ankhFile.Path)

	if len(ankhFile.Charts) > 0 {
		ctx.Logger.Debugf("templating charts")
		for _, chart := range ankhFile.Charts {
			ctx.Logger.Debugf("templating chart '%s'", chart.Name)

			chartOutput, err := templateChart(ctx, chart, ankhFile, ankhConfig)
			if err != nil {
				return finalOutput, err
			}
			finalOutput += chartOutput
		}
	}

	return finalOutput, nil
}
