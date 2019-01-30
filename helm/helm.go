package helm

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v2"

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

func findChartFilesImpl(ctx *ankh.ExecutionContext, chart ankh.Chart) (ankh.ChartFiles, error) {
	files := ankh.ChartFiles{}
	name := chart.Name
	version := chart.Version

	chartPath := chart.Path
	dirErr := os.ErrNotExist
	if version == "" && chartPath != "" {
		if ctx.WorkingPath != "" {
			chartPath = filepath.Join(ctx.WorkingPath, chart.Path)
			ctx.Logger.Debugf("Using chartPath %v since WorkingPath is %v",
				chartPath, ctx.WorkingPath)
		}
		ctx.Logger.Debugf("Considering directory %v for chart %v", chartPath, name)
		_, dirErr = os.Stat(chartPath)
		if dirErr != nil {
			return files, fmt.Errorf("Could not use directory %v for chart %v: %v",
				chartPath, name, dirErr)
		}
	}

	// Setup a directory where we'll either copy the chart files, if we've got a
	// directory, or we'll download and extract a tarball to the temp dir. Then
	// we'll mutate some of the ankh specific files based on the current
	// environment and resource profile. Then we'll use those files as arguments
	// to the helm command.
	tmpDir, err := ioutil.TempDir(ctx.DataDir, name+"-")
	if err != nil {
		return files, err
	}

	// If we already have a dir, let's just copy it to a temp directory so we can
	// make changes to the ankh specific yaml files before passing them as `-f`
	// args to `helm template`
	if dirErr == nil {
		if err := util.CopyDir(chartPath, filepath.Join(tmpDir, name)); err != nil {
			return files, err
		}
	} else {
		// Check for registies in the following order of precedence:
		// - global, context, chart.
		registry := ctx.AnkhConfig.Helm.Registry
		if ctx.AnkhConfig.CurrentContext.HelmRegistryURL != "" {
			// TODO: Deprecate me
			registry = ctx.AnkhConfig.CurrentContext.HelmRegistryURL
		}
		if chart.HelmRegistry != "" {
			registry = chart.HelmRegistry
		}
		if registry == "" {
			return files, fmt.Errorf("No helm registry configured. Set `helm.registry` globally, or see README.md on where to specify a helm registry.")
		}

		// We cannot pull down a chart without a version
		if version == "" {
			return files, fmt.Errorf("Cannot template chart '%v' without a version", chart.Name)
		}

		tarballFileName := fmt.Sprintf("%s-%s.tgz", name, version)
		tarballURL := fmt.Sprintf("%s/%s", strings.TrimRight(registry, "/"), tarballFileName)

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
				ctx.Logger.Warningf("Received HTTP status '%v' (code %v) when trying to call %s (attempt %v)", resp.Status, resp.StatusCode, tarballURL, attempt)
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
		MetaPath:                 filepath.Join(chartDir, "ankh.yaml"),
		ValuesPath:               filepath.Join(chartDir, "values.yaml"),
		AnkhValuesPath:           filepath.Join(chartDir, "ankh-values.yaml"),
		AnkhResourceProfilesPath: filepath.Join(chartDir, "ankh-resource-profiles.yaml"),
		AnkhReleasesPath:         filepath.Join(chartDir, "ankh-releases.yaml"),
	}

	return files, nil
}

var findChartFiles = findChartFilesImpl
var execContext = exec.Command

func FetchChartMeta(ctx *ankh.ExecutionContext, chart *ankh.Chart) (ankh.ChartMeta, error) {
	meta := ankh.ChartMeta{}

	files, err := findChartFiles(ctx, *chart)
	if err != nil {
		return meta, err
	}

	// Load `meta` from chart
	_, metaErr := os.Stat(files.MetaPath)
	if metaErr == nil {
		metaFile, err := os.Open(files.MetaPath)
		if err != nil {
			return meta, fmt.Errorf("unable to process ankh.yaml file for chart '%s': %v", chart.Name, err)
		}

		metaFileContent, err := ioutil.ReadAll(metaFile)
		if err != nil {
			return meta, fmt.Errorf("unable to read contents of ankh.yaml file for chart '%s': %v", chart.Name, err)
		}

		err = yaml.Unmarshal(metaFileContent, &meta)
		if err != nil {
			return meta, fmt.Errorf("unable to unmarshal yaml of ankh.yaml file for chart '%s': %v", chart.Name, err)
		}

		return meta, nil

	}

	return meta, nil
}

func templateChart(ctx *ankh.ExecutionContext, chart ankh.Chart, namespace string) (string, error) {
	currentContext := ctx.AnkhConfig.CurrentContext
	helmArgs := []string{ctx.AnkhConfig.Helm.Command, "template"}

	if namespace != "" {
		helmArgs = append(helmArgs, []string{"--namespace", namespace}...)
	}

	if currentContext.Release != "" {
		helmArgs = append(helmArgs, []string{"--name", currentContext.Release}...)
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

	files, err := findChartFiles(ctx, chart)

	if err != nil {
		return "", err
	}

	// Load `values` from chart
	_, valuesErr := os.Stat(files.AnkhValuesPath)
	if valuesErr == nil {
		if _, err := util.CreateReducedYAMLFile(files.AnkhValuesPath, currentContext.EnvironmentClass, true); err != nil {
			return "", fmt.Errorf("unable to process ankh-values.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", files.AnkhValuesPath)
	}

	// Load `resource-profiles` from chart
	_, resourceProfilesError := os.Stat(files.AnkhResourceProfilesPath)
	if resourceProfilesError == nil {
		if _, err := util.CreateReducedYAMLFile(files.AnkhResourceProfilesPath, currentContext.ResourceProfile, true); err != nil {
			return "", fmt.Errorf("unable to process ankh-resource-profiles.yaml file for chart '%s': %v", chart.Name, err)
		}
		helmArgs = append(helmArgs, "-f", files.AnkhResourceProfilesPath)
	}

	// Load `releases` from chart
	if currentContext.Release != "" {
		_, releasesError := os.Stat(files.AnkhReleasesPath)
		if releasesError == nil {
			out, err := util.CreateReducedYAMLFile(files.AnkhReleasesPath, currentContext.Release, false)
			if err != nil {
				return "", fmt.Errorf("unable to process ankh-releases.yaml file for chart '%s': %v", chart.Name, err)
			}
			if len(out) > 0 {
				helmArgs = append(helmArgs, "-f", files.AnkhReleasesPath)
			}
		}
	}

	// Load `default-values`
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

	// Load `values`
	if chart.Values != nil {
		values, err := util.MapSliceRegexMatch(chart.Values, currentContext.EnvironmentClass)
		if err != nil {
			return "", fmt.Errorf("Failed to load `values` for chart %v: %v", chart.Name, err)
		}
		if values != nil {
			valuesPath := filepath.Join(files.Dir, "values.yaml")
			valuesBytes, err := yaml.Marshal(values)
			if err != nil {
				return "", err
			}

			if err := ioutil.WriteFile(valuesPath, valuesBytes, 0644); err != nil {
				return "", err
			}

			helmArgs = append(helmArgs, "-f", valuesPath)
		}
	}

	// Load `resource-profiles`
	if chart.ResourceProfiles != nil {
		values, err := util.MapSliceRegexMatch(chart.ResourceProfiles, currentContext.ResourceProfile)
		if err != nil {
			return "", fmt.Errorf("Failed to load `resource-profiles` for chart %v: %v", chart.Name, err)
		}
		if values != nil {
			resourceProfilesPath := filepath.Join(files.Dir, "resource-profiles.yaml")
			resourceProfilesBytes, err := yaml.Marshal(values)

			if err != nil {
				return "", err
			}

			if err := ioutil.WriteFile(resourceProfilesPath, resourceProfilesBytes, 0644); err != nil {
				return "", err
			}

			helmArgs = append(helmArgs, "-f", resourceProfilesPath)
		}
	}

	// Load `releases`
	if chart.Releases != nil {
		values, err := util.MapSliceRegexMatch(chart.Releases, currentContext.Release)
		if err != nil {
			return "", fmt.Errorf("Failed to load `releases` for chart %v: %v", chart.Name, err)
		}
		if values != nil {
			releasesPath := filepath.Join(files.Dir, "releases.yaml")
			releasesBytes, err := yaml.Marshal(values)

			if err != nil {
				return "", err
			}

			if err := ioutil.WriteFile(releasesPath, releasesBytes, 0644); err != nil {
				return "", err
			}

			helmArgs = append(helmArgs, "-f", releasesPath)
		}
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

func Version(ctx *ankh.ExecutionContext) (string, error) {
	helmArgs := []string{ctx.AnkhConfig.Helm.Command, "version", "--client"}
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

type HelmReducedEntry struct {
	Created string
	Version string
}

type HelmIndexEntry struct {
	Name    string
	Version string
	Created string
}

type HelmIndex struct {
	ApiVersion string
	Entries    map[string][]HelmIndexEntry
}

func listCharts(ctx *ankh.ExecutionContext, numToShow int, descending bool) (map[string][]string, error) {
	// TODO: Eventually, only support the global helm registry
	registry := ctx.AnkhConfig.Helm.Registry
	if registry == "" {
		registry = ctx.AnkhConfig.CurrentContext.HelmRegistryURL
	}
	if registry == "" {
		return nil, fmt.Errorf("No helm registry configured. Set `helm.registry` globally, or `See README.md on where to specify a helm registry.")
	}

	indexURL := fmt.Sprintf("%s/index.yaml", strings.TrimRight(registry, "/"))
	ctx.Logger.Debugf("downloading index.yaml from %s", indexURL)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   time.Duration(5 * time.Second),
	}
	resp, err := client.Get(indexURL)
	if err != nil {
		return nil, fmt.Errorf("got an error %v when trying to call %v", err, indexURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Received HTTP status '%v' (code %v) when trying to call %s", resp.Status, resp.StatusCode, indexURL)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	index := HelmIndex{}
	err = yaml.Unmarshal(body, &index)
	if err != nil {
		return nil, err
	}

	// Group all entries together, by chart.
	// Sort them by creation date, and then truncate to `numToShow`
	reduced := make(map[string][]string)
	for k, v := range index.Entries {
		sort.Slice(v, func(i, j int) bool {
			lessThan := strings.Compare(v[i].Created, v[j].Created) <= 0
			if descending {
				return !lessThan
			}
			return lessThan
		})
		for _, e := range v {
			reduced[k] = append(reduced[k], e.Version)
		}
		if numToShow > 0 && len(v) > numToShow {
			reduced[k] = reduced[k][:numToShow]
		}
	}

	return reduced, nil
}

func ListCharts(ctx *ankh.ExecutionContext, numToShow int) (string, error) {
	reduced, err := listCharts(ctx, numToShow, true)
	if err != nil {
		return "", err
	}

	// Show charts in alphabetical order
	reducedKeys := []string{}
	for k, _ := range reduced {
		reducedKeys = append(reducedKeys, k)
	}
	sort.Strings(reducedKeys)

	formatted := bytes.NewBufferString("")
	w := tabwriter.NewWriter(formatted, 0, 8, 8, ' ', 0)
	fmt.Fprintf(w, "NAME\tVERSION(S)\n")
	for _, k := range reducedKeys {
		v := reduced[k]
		fmt.Fprintf(w, "%v\t%v\n", k, strings.Join(v, ", "))
	}
	w.Flush()
	return formatted.String(), nil
}

func GetChartNames(ctx *ankh.ExecutionContext) ([]string, error) {
	reducedKeys := []string{}

	reduced, err := listCharts(ctx, 0, true)
	if err != nil {
		return reducedKeys, err
	}

	// Show charts in alphabetical order
	for k, _ := range reduced {
		reducedKeys = append(reducedKeys, k)
	}
	sort.Strings(reducedKeys)
	return reducedKeys, nil
}

func ListVersions(ctx *ankh.ExecutionContext, chart string, descending bool) (string, error) {
	reduced, err := listCharts(ctx, 0, descending)
	if err != nil {
		return "", err
	}

	// Show charts in alphabetical order
	versions, ok := reduced[chart]
	if !ok || len(versions) == 0 {
		return "", fmt.Errorf("Could not find chart '%v' in registry '%v'. "+
			"Try `ankh chart ls` to see all charts and their versions.",
			chart, ctx.AnkhConfig.Helm.Registry)
	}

	return strings.Join(versions, "\n"), nil
}

type ChartYaml struct {
	Name    string
	Version string
}

func readChartYaml(ctx *ankh.ExecutionContext, path string) (map[string]interface{}, ChartYaml, error) {
	rawYaml := make(map[string]interface{})
	chartYaml := ChartYaml{}

	wd, _ := os.Getwd()
	chartFile, err := os.Open("Chart.yaml")
	if err != nil {
		return rawYaml, chartYaml, fmt.Errorf("Could not read Chart.yaml in the current working directly '%v' (error = `%v`). "+
			"Please run from a valid Chart directory containing Chart.yaml. "+
			"See upstream Helm documentation on the Chart file structure for more information.",
			wd, err)
	}

	chartFileContent, err := ioutil.ReadAll(chartFile)
	if err != nil {
		return rawYaml, chartYaml, err
	}

	err = yaml.Unmarshal(chartFileContent, &rawYaml)
	if err != nil {
		return rawYaml, chartYaml, err
	}

	name, ok := rawYaml["name"].(string)
	if !ok {
		return rawYaml, chartYaml, fmt.Errorf("Chart.yaml missing `Name`, or its type is not a string.")
	}

	version, ok := rawYaml["version"].(string)
	if !ok {
		return rawYaml, chartYaml, fmt.Errorf("Chart.yaml missing `version`, or its type is not a string.")
	}

	chartYaml = ChartYaml{
		Name:    name,
		Version: version,
	}

	err = yaml.Unmarshal(chartFileContent, &chartYaml)
	if err != nil {
		return rawYaml, chartYaml, fmt.Errorf("Could not parse Chart.yaml to get `name` and `version` fields: %v", err)
	}

	return rawYaml, chartYaml, nil

}

func writeChartYaml(ctx *ankh.ExecutionContext, chartYaml map[string]interface{}, path string) error {
	wd, _ := os.Getwd()
	chartFile, err := os.Create("Chart.yaml")
	if err != nil {
		return fmt.Errorf("Could not read Chart.yaml in the current working directly '%v' (error = `%v`). "+
			"Please run from a valid Chart directory containing Chart.yaml. "+
			"See upstream Helm documentation on the Chart file structure for more information.",
			wd, err)
	}

	bytes, err := yaml.Marshal(&chartYaml)
	if err != nil {
		return err
	}

	_, err = chartFile.Write(bytes)
	if err != nil {
		return err
	}

	return nil
}

func Publish(ctx *ankh.ExecutionContext) error {
	_, chartYaml, err := readChartYaml(ctx, "Chart.yaml")
	if err != nil {
		return err
	}

	wd, _ := os.Getwd()
	localTarballPath := fmt.Sprintf("%v/%v-%v.tgz", wd, chartYaml.Name, chartYaml.Version)
	removeTarball := func() {
		err = os.Remove(localTarballPath)
		if err != nil && !os.IsNotExist(err) {
			ctx.Logger.Warnf("Error removing tarball '%s': %v", localTarballPath, err)
		}
	}

	// Remove any existing package file now, just in case.
	// Also, clean up at the end of this function.
	removeTarball()
	defer removeTarball()

	helmArgs := []string{ctx.AnkhConfig.Helm.Command, "package", wd}
	helmCmd := execContext(helmArgs[0], helmArgs[1:]...)

	var stderr bytes.Buffer
	helmCmd.Stderr = &stderr

	// Use helm to create a package tarball
	ctx.Logger.Infof("Packaging '%v-%v'", chartYaml.Name, chartYaml.Version)
	err = helmCmd.Run()
	var helmError = string(stderr.Bytes())
	if err != nil {
		outputMsg := ""
		if len(helmError) > 0 {
			outputMsg = fmt.Sprintf(" -- the helm process had the following output on stderr:\n%s", helmError)
		}
		return fmt.Errorf("error running helm command '%v': %v%v",
			strings.Join(helmCmd.Args, " "), err, outputMsg)
	}
	ctx.Logger.Infof("Finished packaging '%v-%v'", chartYaml.Name, chartYaml.Version)

	// Open up and read the contents of the package in order to PUT it upstream
	localTarballFile, err := os.Open(localTarballPath)
	if err != nil {
		return fmt.Errorf("Failed to open packaged chart tarball at path '%v' "+
			"after running helm command '%v' (error = %v)",
			localTarballPath, strings.Join(helmCmd.Args, " "), err)
	}

	body, err := ioutil.ReadAll(localTarballFile)
	if err != nil {
		return err
	}

	upstreamTarballPath := fmt.Sprintf("%v/%v-%v.tgz", ctx.AnkhConfig.Helm.Registry, chartYaml.Name, chartYaml.Version)
	ctx.Logger.Infof("Publishing '%v'", upstreamTarballPath)

	// Create a request with the chart on the PUT body
	req, err := http.NewRequest("PUT", upstreamTarballPath, bytes.NewReader(body))
	if err != nil {
		return err
	}

	switch strings.ToLower(ctx.AnkhConfig.Helm.AuthType) {
	case "basic":
		// Get basic auth credentials
		username := os.Getenv("ANKH_HELM_REGISTRY_USERNAME")
		if username == "" {
			if ctx.NoPrompt {
				return fmt.Errorf("Must define ANKH_HELM_REGISTRY_USERNAME for \"basic\" auth if run with `--no-prompt`")
			}
			username, err = util.PromptForUsername()
			if err != nil {
				return fmt.Errorf("Failed to read credentials from stdin: %v", err)
			}
		} else {
			ctx.Logger.Infof("Using environment ANKH_HELM_REGISTRY_USERNAME=%v for 'basic' auth on helm registry '%v",
				username, ctx.AnkhConfig.Helm.Registry)
		}

		password := os.Getenv("ANKH_HELM_REGISTRY_PASSWORD")
		if password == "" {
			if ctx.NoPrompt {
				return fmt.Errorf("Must define ANKH_HELM_REGISTRY_PASSWORD for \"basic\" if run with `--no-prompt`")
			}
			password, err = util.PromptForPassword()
			if err != nil {
				return fmt.Errorf("Failed to read credentials from stdin: %v", err)
			}
		} else {
			ctx.Logger.Infof("Using environment ANKH_HELM_REGISTRY_PASSWORD=<redacted> for 'basic' auth on helm registry '%v",
				ctx.AnkhConfig.Helm.Registry)
		}

		req.SetBasicAuth(username, password)
	default:
		if ctx.AnkhConfig.Helm.AuthType != "" {
			ctx.Logger.Fatalf("Helm registry auth type '%v' is not supported - only 'basic' auth is supported.", ctx.AnkhConfig.Helm.AuthType)
		}
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: time.Duration(5 * time.Second),
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("got an error %v when trying to PUT %v", err, upstreamTarballPath)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Received HTTP status '%v' (code %v) when trying to PUT %s",
			resp.Status, resp.StatusCode, upstreamTarballPath)
	}

	ctx.Logger.Debug("Helm registry PUT resp: %+v", resp)
	ctx.Logger.Infof("Finished publishing '%v'", upstreamTarballPath)
	return nil
}

func Template(ctx *ankh.ExecutionContext, charts []ankh.Chart, namespace string) (string, error) {
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

func inspectFile(relativeDir string, file string) (string, error) {
	result := fmt.Sprintf("\n---\n# Source: %s/%s\n", relativeDir, path.Base(file))
	bytes, err := ioutil.ReadFile(file)
	if err != nil {
		return "", err
	}

	return result + string(bytes), nil
}

func inspectDirectory(relativeDir string, dir string) (string, error) {
	var r, result string
	var files []os.FileInfo
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return "", err
	}

	for _, f := range files {
		path := fmt.Sprintf("%v/%v", dir, f.Name())
		if f.IsDir() {
			r, err = inspectDirectory(fmt.Sprintf("%v/%v", relativeDir, f.Name()), path)
		} else {
			r, err = inspectFile(relativeDir, path)
		}
		if err != nil {
			return "", err
		}
		result += r
	}
	return result, nil
}

func Inspect(ctx *ankh.ExecutionContext, singleChart string) (string, error) {
	var result string

	tokens := strings.Split(singleChart, "@")
	if len(tokens) < 1 || len(tokens) > 2 {
		ctx.Logger.Fatalf("Invalid chart '%v'.  Chart must be specified as `CHART[@VERSION]`.",
			singleChart)
	}

	chartName := tokens[0]
	chartVersion := ""
	if len(tokens) == 2 {
		chartVersion = tokens[1]
	} else {
		versions, err := ListVersions(ctx, chartName, true)
		if err != nil {
			return "", err
		}

		ctx.Logger.Infof("Found chart \"%v\" without a version", chartName)
		selectedVersion, err := util.PromptForSelection(strings.Split(strings.Trim(versions, "\n "), "\n"),
			fmt.Sprintf("Select a version for chart '%v'", chartName))
		if err != nil {
			return "", err
		}

		chartVersion = selectedVersion
		ctx.Logger.Infof("Using %v@%v based on selection", chartName, chartVersion)
	}

	ctx.Logger.Infof("Inspecting chart \"%s\" at version \"%v\" from registry \"%v\"",
		chartName, chartVersion, ctx.AnkhConfig.Helm.Registry)

	chart := ankh.Chart{
		Name:    chartName,
		Version: chartVersion,
	}
	files, err := findChartFiles(ctx, chart)
	if err != nil {
		return "", err
	}

	// Inspect everything inside the Chart directory
	inspection, err := inspectDirectory(chartName, files.ChartDir)
	if err != nil {
		return "", err
	}

	result += inspection
	return result, nil
}

func Bump(ctx *ankh.ExecutionContext, semVerType string) error {
	rawYaml, chartYaml, err := readChartYaml(ctx, "Chart.yaml")
	if err != nil {
		return err
	}

	ctx.Logger.Infof("Found version \"%v\" in Chart.yaml. Bumping \"%v\" version...",
		chartYaml.Version, semVerType)
	newVersion, err := util.SemverBump(chartYaml.Version, semVerType)
	if err != nil {
		return fmt.Errorf("Could not bump version using semantic versioning. See https://semver.org for the semantic version spec. Original error: %v", err)
	}

	rawYaml["version"] = newVersion

	ctx.Logger.Infof("Writing new version \"%v\" to Chart.yaml", newVersion)
	err = writeChartYaml(ctx, rawYaml, "Chart.yaml")
	if err != nil {
		return err
	}

	return nil
}
