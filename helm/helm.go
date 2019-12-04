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
	"github.com/appnexus/ankh/plan"
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

func findChartFilesImpl(ctx *ankh.ExecutionContext, repository string, chart ankh.Chart) (ankh.ChartFiles, error) {
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
		// Override the provided repository if it is specified at the Chart level
		if chart.HelmRepository != "" {
			repository = chart.HelmRepository
		}
		if repository == "" {
			return files, fmt.Errorf("No helm repository configured. Set `helm.repository` globally, or see README.md on where to specify a helm repository.")
		}

		// We cannot pull down a chart without a version
		if version == "" {
			return files, fmt.Errorf("Cannot template chart '%v' without a version", chart.Name)
		}

		tarballFileName := fmt.Sprintf("%s-%s.tgz", name, version)
		tarballURL := fmt.Sprintf("%s/%s", strings.TrimRight(repository, "/"), tarballFileName)

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
		TmpDir:                   tmpDir,
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

func FetchChartMeta(ctx *ankh.ExecutionContext, repository string, chart *ankh.Chart) (ankh.ChartMeta, error) {
	meta := ankh.ChartMeta{}

	files, err := findChartFiles(ctx, repository, *chart)
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

func Version(ctx *ankh.ExecutionContext) (string, error) {
	cmd := plan.NewCommand(ctx.AnkhConfig.Helm.Command)
	cmd.AddArguments([]string{"version", "--client", "--short"})
	// We want to return the output of the version command in Run, so use a pipe
	cmd.PipeStdoutAndStderr = plan.PIPE_TYPE_PIPE
	return cmd.Run(ctx, nil)
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

func listCharts(ctx *ankh.ExecutionContext, repository string, numToShow int, descending bool) (map[string][]string, error) {
	if repository == "" {
		return nil, fmt.Errorf("No helm repository configured. Set `helm.repository` globally, or `See README.md on where to specify a helm repository.")
	}

	indexURL := fmt.Sprintf("%s/index.yaml", strings.TrimRight(repository, "/"))
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

func ListCharts(ctx *ankh.ExecutionContext, repository string, numToShow int) (string, error) {
	reduced, err := listCharts(ctx, repository, numToShow, true)
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

func GetChartNames(ctx *ankh.ExecutionContext, repository string) ([]string, error) {
	reducedKeys := []string{}

	reduced, err := listCharts(ctx, repository, 0, true)
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

func ListVersions(ctx *ankh.ExecutionContext, repository string, chart string, descending bool) (string, error) {
	reduced, err := listCharts(ctx, repository, 0, descending)
	if err != nil {
		return "", err
	}

	// Show charts in alphabetical order
	versions, ok := reduced[chart]
	if !ok || len(versions) == 0 {
		return "", fmt.Errorf("Could not find chart '%v' in repository '%v'. "+
			"Try `ankh chart ls` to see all charts and their versions.",
			chart, repository)
	}

	return strings.Join(versions, "\n"), nil
}

type ChartYaml struct {
	Name    string
	Version string
}

func readChartYaml(ctx *ankh.ExecutionContext, path string, lenient bool) (map[string]interface{}, ChartYaml, error) {
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
		if lenient {
			version = ""
		} else {
			return rawYaml, chartYaml, fmt.Errorf("Chart.yaml missing `version`, or its type is not a string.")
		}
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

func Publish(ctx *ankh.ExecutionContext, repository string, versionOverride string) error {
	_, chartYaml, err := readChartYaml(ctx, "Chart.yaml", true)
	if err != nil {
		return err
	}

	chartName := chartYaml.Name
	chartVersion := chartYaml.Version
	if versionOverride != "" {
		chartVersion = versionOverride
		ctx.Logger.Infof("Using chart version %v from command line", versionOverride)
	} else {
		ctx.Logger.Infof("Using chart version %v from Chart.yaml", chartVersion)
	}

	wd, _ := os.Getwd()
	localTarballPath := fmt.Sprintf("%v/%v-%v.tgz", wd, chartName, chartVersion)
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

	helmArgs := []string{ctx.AnkhConfig.Helm.Command, "package"}
	if versionOverride != "" {
		helmArgs = append(helmArgs, []string{"--version", versionOverride}...)
	}
	helmArgs = append(helmArgs, wd)
	helmCmd := execContext(helmArgs[0], helmArgs[1:]...)

	var stderr bytes.Buffer
	helmCmd.Stderr = &stderr

	// Use helm to create a package tarball
	ctx.Logger.Infof("Packaging '%v-%v'", chartName, chartVersion)
	ctx.Logger.Debugf("Running command %v", helmCmd)
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
	ctx.Logger.Infof("Finished packaging '%v-%v'", chartName, chartVersion)

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

	upstreamTarballPath := fmt.Sprintf("%v/%v-%v.tgz", repository, chartName, chartVersion)
	ctx.Logger.Infof("Publishing '%v'", upstreamTarballPath)

	// Create a request with the chart on the PUT body
	req, err := http.NewRequest("PUT", upstreamTarballPath, bytes.NewReader(body))
	if err != nil {
		return err
	}

	switch strings.ToLower(ctx.AnkhConfig.Helm.AuthType) {
	case "basic":
		// Get basic auth credentials
		username := os.Getenv("ANKH_HELM_REPOSITORY_USERNAME")
		if username == "" {
			if ctx.NoPrompt {
				return fmt.Errorf("Must define ANKH_HELM_REPOSITORY_USERNAME for \"basic\" auth if run with `--no-prompt`")
			}
			username, err = util.PromptForUsernameWithLabel("Username: ")
			if err != nil {
				return fmt.Errorf("Failed to read credentials from stdin: %v", err)
			}
		} else {
			ctx.Logger.Infof("Using environment ANKH_HELM_REPOSITORY_USERNAME=%v for 'basic' auth on helm repository '%v",
				username, repository)
		}

		password := os.Getenv("ANKH_HELM_REPOSITORY_PASSWORD")
		if password == "" {
			if ctx.NoPrompt {
				return fmt.Errorf("Must define ANKH_HELM_REPOSITORY_PASSWORD for \"basic\" if run with `--no-prompt`")
			}
			password, err = util.PromptForPasswordWithLabel("Password: ")
			if err != nil {
				return fmt.Errorf("Failed to read credentials from stdin: %v", err)
			}
		} else {
			ctx.Logger.Infof("Using environment ANKH_HELM_REPOSITORY_PASSWORD=<redacted> for 'basic' auth on helm repository '%v",
				repository)
		}

		req.SetBasicAuth(username, password)
	default:
		if ctx.AnkhConfig.Helm.AuthType != "" {
			ctx.Logger.Fatalf("Helm repository auth type '%v' is not supported - only 'basic' auth is supported.", ctx.AnkhConfig.Helm.AuthType)
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

	ctx.Logger.Debugf("Helm repository PUT resp: %+v", resp)
	ctx.Logger.Infof("Finished publishing '%v'", upstreamTarballPath)
	return nil
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

func Inspect(ctx *ankh.ExecutionContext, repository string, singleChart string) (string, error) {
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
		versions, err := ListVersions(ctx, repository, chartName, true)
		if err != nil {
			return "", err
		}

		ctx.Logger.Infof("Found chart \"%v\" without a version", chartName)
		selectedVersion, err := util.PromptForSelection(strings.Split(strings.Trim(versions, "\n "), "\n"),
			fmt.Sprintf("Select a version for chart '%v'", chartName), false)
		if err != nil {
			return "", err
		}

		chartVersion = selectedVersion
		ctx.Logger.Infof("Using %v@%v based on selection", chartName, chartVersion)
	}

	ctx.Logger.Infof("Inspecting chart \"%s\" at version \"%v\" from repository \"%v\"",
		chartName, chartVersion, repository)

	chart := ankh.Chart{
		Name:    chartName,
		Version: chartVersion,
	}
	files, err := findChartFiles(ctx, repository, chart)
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
	rawYaml, chartYaml, err := readChartYaml(ctx, "Chart.yaml", false)
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

func filterOutput(filters []string, helmOutput string) string {

	// The golang yaml library doesn't actually support whitespace/comment
	// preserving round-trip parsing. So, we're going to filter the "hard way".
	filtered := []string{}
	objs := strings.Split(helmOutput, "\n---")
	for _, obj := range objs {
		lines := strings.Split(obj, "\n")
		for _, line := range lines {
			if !strings.HasPrefix(line, "kind:") {
				continue
			}
			matched := false
			for _, s := range filters {
				kind := strings.Trim(line[5:], " ")
				if strings.EqualFold(kind, s) {
					matched = true
					break
				}
			}
			if matched {
				filtered = append(filtered, obj)
				break
			}
		}
	}

	output := ""
	for _, s := range filtered {
		output += fmt.Sprintf("---\n%v\n", strings.Trim(s, "\n"))
	}
	return output
}

// CreateChart via helm create that is ankh compatible
func CreateChart(ctx *ankh.ExecutionContext, chartPath string, appName string, tagImage string, repositoryArg string) error {
	var err error

	// Setup Defaults
	chartRoot := "helm"
	appName = util.GenerateName(ctx, appName)
	chartDir := fmt.Sprintf("%v/%v", chartRoot, appName)
	helmArgs := []string{}
	repository := ctx.DetermineHelmRepository(&repositoryArg)

	// Evaluate params passed in
	if chartPath != "" {
		chartRoot = chartPath
		chartDir = chartPath
		if idx := strings.Index(chartPath, "/"); idx != -1 {
			chartRoot = chartPath[:idx]
			appName = chartPath[idx+1:]
			ctx.Logger.Infof("Using chart name (%v) from chart path", appName)
		}
	}

	ctx.Logger.Infof("Creating chart with name: %v", appName)

	// Only create chart if the root directory does not already exist
	if _, err := os.Stat(chartRoot); !os.IsNotExist(err) {
		ctx.Logger.Infof("Chart directory %v already exists. Ready to go!", chartRoot)
		return nil
	}

	// Create the root directory before adding chart
	os.Mkdir(chartRoot, os.ModePerm)

	// Make sure we have a chart to look for
	if ctx.Chart == "" {
		if repository == "" || ctx.NoPrompt {
			return fmt.Errorf("No starter chart specified, unable to create chart")
		}

		// Prompt for a chart
		ctx.Logger.Infof("No starter-chart specified as an argument")
		charts, err := GetChartNames(ctx, repository)
		if err != nil {
			return err
		}

		selectedChart, err := util.PromptForSelection(charts, "Select a chart", false)
		if err != nil {
			return err
		}

		ctx.Chart = selectedChart
	}

	// Currently the only way to add a chart to $HELM_HOME/starters (and therefore use it) is to manually copy it there.
	// Only copy if the chart does not already exist
	chartStarterPath := path.Join(ctx.HelmDir, "starters/", ctx.Chart)
	if _, err := os.Stat(chartStarterPath); os.IsNotExist(err) {
		tokens := strings.Split(ctx.Chart, "@")
		if len(tokens) > 2 {
			ctx.Logger.Fatalf("Invalid chart '%v'. Too many `@` characters found. Chart must either be a name with no `@`, or in the combined `name@version` format", ctx.Chart)
		}
		if len(tokens) == 1 {
			versions, err := ListVersions(ctx, repository, ctx.Chart, true)
			if err != nil {
				return err
			}
			selectedVersion, err := util.PromptForSelection(strings.Split(strings.Trim(versions, "\n "), "\n"),
				fmt.Sprintf("Select a version for chart \"%v\"", ctx.Chart), false)
			if err != nil {
				return err
			}
			ctx.Chart = fmt.Sprintf("%v@%v", ctx.Chart, selectedVersion)
		}

		// Check existence again with version number
		chartStarterPath = path.Join(ctx.HelmDir, "starters/", ctx.Chart)
		if _, err := os.Stat(chartStarterPath); os.IsNotExist(err) {

			// Get chart from remote repository
			ankhFile, err := ankh.GetAnkhFile(ctx)
			if err != nil {
				return err
			}
			chart := &ankhFile.Charts[0]

			files, err := findChartFiles(ctx, repository, *chart)
			if err != nil {
				return err
			}

			err = util.CopyDir(files.ChartDir, chartStarterPath)

			ctx.Logger.Infof("The chart was pulled from the repository and stored at %v", chartStarterPath)
		}
	}

	ctx.Logger.Infof("Here we go, creating chart based on: %v", ctx.Chart)

	// $HELM_HOME must be set for helm create to work, make sure this is set before continuting
	os.Setenv("HELM_HOME", ctx.HelmDir)
	helmArgs = []string{ctx.AnkhConfig.Helm.Command, "create", chartDir, "--starter", ctx.Chart}
	helmCmd := execContext(helmArgs[0], helmArgs[1:]...)

	var stderr bytes.Buffer
	helmCmd.Stderr = &stderr

	// run helm to create chart
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

	// Make updates to ankh.yaml based on app and flags
	if ctx.Namespace != nil {
		ctx.Logger.Infof("Updating Namespace in ankh.yaml to %v", *ctx.Namespace)
		originalString := "namespace: default"
		namespaceString := fmt.Sprintf("namespace: %v", *ctx.Namespace)
		filename := fmt.Sprintf("%v/%v", chartDir, "ankh.yaml")
		if err = util.UpdateFile(filename, namespaceString, originalString); err != nil {
			return err
		}
	}

	imageName := appName
	if tagImage != "" {
		imageName = tagImage
	}

	ctx.Logger.Infof("Updating tag image in ankh.yaml to %v", imageName)
	originalImage := "tagImage: appname"
	updatedImage := fmt.Sprintf("tagImage: %v", imageName)
	filename := fmt.Sprintf("%v/%v", chartDir, "ankh.yaml")
	if err = util.UpdateFile(filename, updatedImage, originalImage); err != nil {
		return err
	}

	ctx.Logger.Infof("Finished creating chart")

	return nil
}
