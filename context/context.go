package ankh

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type Mode string

const (
	Apply    Mode = "apply"
	Explain  Mode = "explain"
	Deploy   Mode = "deploy"
	Rollback Mode = "rollback"
	Diff     Mode = "diff"
	Exec     Mode = "exec"
	Get      Mode = "get"
	Pods     Mode = "pods"
	Lint     Mode = "lint"
	Logs     Mode = "logs"
	Template Mode = "template"
)

// Captures all of the context required to execute a single iteration of Ankh
type ExecutionContext struct {
	AnkhConfig AnkhConfig

	AnkhFilePath string
	Chart        string
	LocalChart   bool
	Tag          *string
	Namespace    *string

	Mode Mode

	Verbose, Quiet, ShouldCatchSignals, CatchSignals, DryRun, Describe, WarnOnConfigError,
	IgnoreContextAndEnv, IgnoreConfigErrors, SkipConfig, NoPrompt bool

	WorkingPath    string
	AnkhConfigPath string
	KubeConfigPath string
	Context        string
	Release        string
	Environment    string
	DataDir        string
	HelmSetValues  map[string]string
	HelmDir        string

	DeploymentTag string

	SlackChannel         string
	SlackMessageOverride string

	CreateJiraTicket bool

	Filters []string

	ImageTagFilter     string
	ChartVersionFilter string

	ExtraArgs, PassThroughArgs []string

	HelmVersion, KubectlVersion string

	HelmV2 bool

	Logger *logrus.Logger
}

// Context is a struct that represents a context for applying files to a
// Kubernetes cluster
type Context struct {
	Source                string                 `yaml:"-"` // private field. specifies which config file declared this.
	KubeContext           string                 `yaml:"kube-context,omitempty"`
	KubeServer            string                 `yaml:"kube-server,omitempty"`
	KubeConfig            string                 `yaml:"kube-config,omitempty"`
	Environment           string                 `yaml:"environment,omitempty"` // deprecated in favor of `environment-class`
	EnvironmentClass      string                 `yaml:"environment-class"`     // omitempty until we remove `environment`
	ResourceProfile       string                 `yaml:"resource-profile"`
	Release               string                 `yaml:"release,omitempty"`
	HelmRegistryURLUnused string                 `yaml:"helm-registry-url,omitempty"`   // deprecated in favor of top-level config `helm.repository`
	HelmRepositoryURL     string                 `yaml:"helm-repository-url,omitempty"` // deprecated in favor of top-level config `helm.repository`
	ClusterAdminUnused    bool                   `yaml:"cluster-admin,omitempty"`       // deprecated
	Global                map[string]interface{} `yaml:"global",omitempty"`
}

// An Environment is a collection of contexts over which operations should be applied
type Environment struct {
	Source   string   `yaml:"-"` // private field. specifies which config file declared this.
	Contexts []string `yaml:"contexts"`
}

type KubectlConfig struct {
	Command        string   `yaml:"command,omitempty"`
	WildCardLabels []string `yaml:"wildCardLabels,omitempty"`
}

type HelmConfig struct {
	Command string `yaml:"command,omitempty"`
	// XXX TODO: Deprecate
	TagValueNameUnused string `yaml:"tagValueName,omitempty"`
	RegistryUnused     string `yaml:"registry,omitempty"`
	Repository         string `yaml:"repository,omitempty"`
	AuthType           string `yaml:"authType,omitempty"`
}

type DockerConfig struct {
	Registry string `yaml:"registry,omitempty"`
}

type SlackConfig struct {
	Token          string `yaml:"token"`
	Icon           string `yaml:"icon-url"`
	Username       string `yaml:"username"`
	Format         string `yaml:"format"`
	RollbackFormat string `yaml:"rollbackFormat"`
	Pretext        string `yaml:"pretext"`
}

type JiraConfig struct {
	Queue                     string `yaml:"queue,omitempty"`
	BaseUrl                   string `yaml:"baseUrl,omitempty"`
	AutoClose                 bool   `yaml:"autoClose"`
	SummaryFormat             string `yaml:"summaryFormat"`
	RollbackSummaryFormat     string `yaml:"rollbackSummaryFormat"`
	DescriptionFormat         string `yaml:"descriptionFormat"`
	RollbackDescriptionFormat string `yaml:"rollbackDescriptionFormat"`
}

// AnkhConfig defines the shape of the ~/.ankh/config file used for global
// configuration options
type AnkhConfig struct {
	Include                           []string               `yaml:"include,omitempty"`
	Environments                      map[string]Environment `yaml:"environments"`
	SupportedEnvironmentsUnused       []string               `yaml:"supported-environments,omitempty"`        // deprecated
	SupportedEnvironmentClassesUnused []string               `yaml:"supported-environment-classes,omitempty"` // deprecated
	SupportedResourceProfilesUnused   []string               `yaml:"supported-resource-profiles,omitempty"`   // deprecated
	CurrentContextNameUnused          string                 `yaml:"current-context,omitempty"`               // deprecated
	CurrentContextName                string                 `yaml:"-"`                                       // deprecated
	CurrentContext                    Context                `yaml:"-"`                                       // deprecated TODO: RENAME TO UNUSED
	Contexts                          map[string]Context     `yaml:"contexts"`

	Kubectl KubectlConfig `yaml:"kubectl,omitempty"`
	Helm    HelmConfig    `yaml:"helm,omitempty"`
	Docker  DockerConfig  `yaml:"docker,omitempty"`
	Slack   SlackConfig   `yaml:"slack,omitempty"`
	Jira    JiraConfig    `yaml:"jira,omitempty"`

	// List of namespace suggestions to use if the user does not provide one when required.
	Namespaces []string `yaml:"namespaces,omitempty"`
}

type KubeCluster struct {
	Cluster struct {
		Server string `yaml:"server"`
	}
	Name string `yaml:"name"`
}

type KubeContext struct {
	Context struct {
		Cluster string `yaml:"cluster"`
	}
	Name string `yaml:"name"`
}

type KubeConfig struct {
	ApiVersion           string        `yaml:"apiVersion"`
	Kind                 string        `yaml:"kind"`
	Clusters             []KubeCluster `yaml:"clusters"`
	Contexts             []KubeContext `yaml:"contexts"`
	CurrentContextUnused string        `yaml:"current-context"` // for serialization purposes only
}

func (ctx *ExecutionContext) DetermineHelmRepository(preferredRepository *string) string {
	// For commands that take command line arguments, the argument is the
	// preferred value. For operations over charts, the chart-level override
	// is the preferred value.
	// TODO: Checking for empty string is a hack. Don't do that. Change chart.HelmRepository to a string* instead.
	if preferredRepository != nil && *preferredRepository != "" {
		return *preferredRepository
	}

	repository := ctx.AnkhConfig.Helm.Repository
	if repository != "" {
		return repository
	}

	repository = ctx.AnkhConfig.CurrentContext.HelmRepositoryURL
	if repository != "" {
		ctx.Logger.Infof("Using repository \"%v\" taken from the current context "+
			"\"%v\"", repository, ctx.AnkhConfig.CurrentContextName)
		return repository
	}

	repository = ctx.AnkhConfig.CurrentContext.HelmRegistryURLUnused
	if repository != "" {
		ctx.Logger.Infof("Using legacy registry config \"%v\" taken from the current context "+
			"\"%v\"", repository, ctx.AnkhConfig.CurrentContextName)
		return repository
	}

	ctx.Logger.Fatalf("No helm repository configured. " +
		"Set `helm.repository` globally, pass it as an argument, or see README.md")
	return ""
}

// This function is so bad
func useKubeConfig(ctx *ExecutionContext, currentContext *Context, name string, kubeConfigBytes []byte) error {
	// Extra forward slashes for the scheme seems wrong. So change them
	// to underscores, or whatever.
	kubeConfigDir := path.Join(ctx.DataDir, "kubeconfig", strings.Replace(currentContext.KubeServer, "/", "_", -1))
	if err := os.MkdirAll(kubeConfigDir, 0755); err != nil {
		return err
	}

	kubeConfigPath := path.Join(kubeConfigDir, "kubeconfig.yaml")

	ctx.Logger.Debugf("Using kubeConfigPath %v", kubeConfigPath)
	if err := ioutil.WriteFile(kubeConfigPath, kubeConfigBytes, 0644); err != nil {
		return err
	}

	currentContext.KubeContext = name
	ctx.KubeConfigPath = kubeConfigPath
	return nil
}

// ValidateAndInit ensures the AnkhConfig is internally sane and populates
// special fields if necessary.
func (ankhConfig *AnkhConfig) ValidateAndInit(ctx *ExecutionContext, context string) []error {
	errors := []error{}

	if context != "" {
		ankhConfig.CurrentContextName = context
	}

	if ankhConfig.CurrentContextName == "" {
		errors = append(errors, fmt.Errorf("Missing or empty `current-context`"))
	}

	selectedContext, contextExists := ankhConfig.Contexts[ankhConfig.CurrentContextName]
	if contextExists == false {
		errors = append(errors, fmt.Errorf("Context '%s' not found in `contexts`", ankhConfig.CurrentContextName))
	} else {
		// Environment (on the context) is deprecated, but we still use it if EnvironmentClass is missing.
		if selectedContext.Environment != "" && selectedContext.EnvironmentClass == "" {
			ctx.Logger.Warnf("Current context '%s' contains field `environment`, which has been deprecated in favor of `environment-class`", ankhConfig.CurrentContextName)
			selectedContext.EnvironmentClass = selectedContext.Environment
		}

		if selectedContext.KubeContext == "" && selectedContext.KubeServer == "" {
			errors = append(errors, fmt.Errorf("Current context '%s' has missing or empty `kube-context` or `kube-server`", ankhConfig.CurrentContextName))
		} else if selectedContext.KubeServer != "" && selectedContext.KubeConfig != "" {
			errors = append(errors, fmt.Errorf("Cannot specify both `kube-server` and `kube-config`"))
		} else if selectedContext.KubeServer != "" {
			kubeCluster := KubeCluster{
				Cluster: struct {
					Server string `yaml:"server"`
				}{Server: selectedContext.KubeServer},
				Name: "_kcluster",
			}
			kubeContext := KubeContext{
				Context: struct {
					Cluster string `yaml:"cluster"`
				}{Cluster: kubeCluster.Name},
				Name: "_kctx",
			}
			kubeConfig := &KubeConfig{
				ApiVersion:           "v1",
				Kind:                 "Config",
				Clusters:             []KubeCluster{kubeCluster},
				Contexts:             []KubeContext{kubeContext},
				CurrentContextUnused: kubeContext.Name,
			}

			kubeConfigBytes, err := yaml.Marshal(kubeConfig)
			if err != nil {
				return []error{err}
			}

			useKubeConfig(ctx, &selectedContext, kubeContext.Name, kubeConfigBytes)
		} else if selectedContext.KubeConfig != "" {
			u, err := url.Parse(selectedContext.KubeConfig)
			if err != nil {
				return []error{fmt.Errorf("Could not parse current context kube-config '%v' as a URL: %v", selectedContext.KubeConfig, err)}
			}

			if u.Scheme == "http" || u.Scheme == "https" {
				resp, err := http.Get(selectedContext.KubeConfig)
				if err != nil {
					return []error{fmt.Errorf("Unable to fetch ankh file from URL '%s': %v", selectedContext.KubeConfig, err)}
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					return []error{fmt.Errorf("Non-200 status code when fetching ankh file from URL '%s': %v", selectedContext.KubeConfig, resp.Status)}
				}
				body, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					return []error{err}
				}
				useKubeConfig(ctx, &selectedContext, selectedContext.KubeContext, body)
			} else {
				ctx.KubeConfigPath = selectedContext.KubeConfig
			}
		}

		if selectedContext.EnvironmentClass == "" {
			errors = append(errors, fmt.Errorf("Current context '%s' has missing or empty `environment-class`", ankhConfig.CurrentContextName))
		}

		if selectedContext.ResourceProfile == "" {
			errors = append(errors, fmt.Errorf("Current context '%s' has missing or empty `resource-profile`", ankhConfig.CurrentContextName))
		}
	}

	ankhConfig.CurrentContext = selectedContext
	if ctx.Release != "" {
		if ankhConfig.CurrentContext.Release != "" {
			ctx.Logger.Warnf("Overriding existing release \"%v\" to release argument \"%v\" from command line for context \"%v\"", ankhConfig.CurrentContext.Release, ctx.Release, ankhConfig.CurrentContextName)
		} else {
			ctx.Logger.Infof("Using release argument \"%v\" from command line for context \"%v\"", ctx.Release, ankhConfig.CurrentContextName)
		}
		ankhConfig.CurrentContext.Release = ctx.Release
	}
	return errors
}

type ConfigMeta struct {
	Type  string            `yaml:"type"`
	Paths map[string]string `yaml:"paths"`
}

type ChartMeta struct {
	Namespace      *string    `yaml:"namespace"`
	TagImage       string     `yaml:"tagImage"`
	TagKey         string     `yaml:"tagKey"`
	WildCardLabels *[]string  `yaml:"wildCardLabels"`
	ConfigMeta     ConfigMeta `yaml:"config"`
}

type ChartFiles struct {
	TmpDir                   string
	ChartDir                 string
	GlobalPath               string
	MetaPath                 string
	ValuesPath               string
	AnkhValuesPath           string
	AnkhResourceProfilesPath string
	AnkhReleasesPath         string
}

type Chart struct {
	Path    string
	Name    string
	Version string
	Tag     *string
	// Overrides any global Helm registry
	HelmRegistryUnused string
	HelmRepository     string
	ChartMeta          ChartMeta `yaml:"meta"`
	// DefaultValues are values that apply unconditionally, with lower precedence than values supplied in the fields below.
	DefaultValues map[string]interface{} `yaml:"default-values"`
	// Values, by environment-class, resource-profile, or release. MapSlice preserves map ordering so we can regex search from top to bottom.
	Values           yaml.MapSlice
	ResourceProfiles yaml.MapSlice `yaml:"resource-profiles"`
	Releases         yaml.MapSlice

	Files *ChartFiles `yaml:"-"` // private, filled in by FetchChart
}

// AnkhFile defines the shape of the `ankh.yaml` file which is used to define
// clusters and their contents
type AnkhFile struct {
	// (private) an absolute path to the ankh.yaml file
	Path string `yaml:"-"`

	// The Kubernetes namespace to apply each chart to, if not overriden
	// on the command line nor on the individual chart object.
	Namespace *string
	Charts    []Chart

	Dependencies []string `yaml:"dependencies"`
}

func ParseAnkhFile(ankhFilePath string) (AnkhFile, error) {
	ankhFile := AnkhFile{}
	u, err := url.Parse(ankhFilePath)
	if err != nil {
		return ankhFile, fmt.Errorf("Could not parse ankhFilePath '%v' as a URL: %v", ankhFilePath, err)
	}

	body := []byte{}
	if u.Scheme == "http" || u.Scheme == "https" {
		resp, err := http.Get(ankhFilePath)
		if err != nil {
			return ankhFile, fmt.Errorf("Unable to fetch ankh file from URL '%s': %v", ankhFilePath, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ankhFile, fmt.Errorf("Non-200 status code when fetching ankh file from URL '%s': %v", ankhFilePath, resp.Status)
		}
		body, err = ioutil.ReadAll(resp.Body)
	} else {
		body, err = ioutil.ReadFile(ankhFilePath)
		if err == nil {
			// Add the absolute path of the ankhFile to the struct
			ankhFile.Path, err = filepath.Abs(ankhFilePath)
		}

	}
	if err != nil {
		return ankhFile, err
	}

	err = yaml.Unmarshal(body, &ankhFile)
	if err != nil {
		return ankhFile, fmt.Errorf("Error loading Ankh file '%v': %v\nPlease refer to README.md for the correct schema of an Ankh file", ankhFilePath, err)
	}

	return ankhFile, nil
}

func GetAnkhFile(ctx *ExecutionContext) (AnkhFile, error) {
	if ctx.Chart == "" {
		if ctx.AnkhFilePath == "" {
			// No ankhfile.
			return AnkhFile{}, nil
		}
		ctx.Logger.Infof("Reading Ankh file %v", ctx.AnkhFilePath)
		ankhFile, err := ParseAnkhFile(ctx.AnkhFilePath)
		if err == nil {
			ctx.Logger.Debugf("- OK: %v", ctx.AnkhFilePath)
			return ankhFile, nil
		} else if os.IsNotExist(err) && ctx.AnkhFilePath == "ankh.yaml" {
			ctx.Logger.Infof("Default Ankh file ankh.yaml not found")
			return AnkhFile{}, nil
		} else {
			return ankhFile, err
		}
	}

	// We have a chart argument, which makes things more complicated.
	return getAnkhFileForChart(ctx, ctx.Chart)
}

type HelmChart struct {
	Name string
}

func readChartDirectory(chartDir string) (*HelmChart, error) {
	chartYamlPath := filepath.Join(chartDir, "Chart.yaml")
	chartYaml, err := ioutil.ReadFile(chartYamlPath)
	if err != nil {
		return nil, err
	}
	helmChart := HelmChart{}
	err = yaml.Unmarshal(chartYaml, &helmChart)
	if err != nil {
		return nil, err
	}
	if helmChart.Name == "" {
		return nil, fmt.Errorf("Did not find any `name` in %v", chartYamlPath)
	}
	return &helmChart, nil
}

func getAnkhFileForChart(ctx *ExecutionContext, singleChart string) (AnkhFile, error) {
	ankhFile := AnkhFile{}

	if ctx.LocalChart {
		// The user wants to use a local chart. Interpret singleChart as a directory.
		ctx.Logger.Infof("Using chart directory %v", singleChart)
		helmChart, err := readChartDirectory(singleChart)
		if err != nil {
			return AnkhFile{}, fmt.Errorf("Could not use \"%v\" as a local chart directory: %v", singleChart, err)
		}

		ankhFile = AnkhFile{
			Charts: []Chart{
				Chart{Path: singleChart, Name: helmChart.Name},
			},
		}
		return ankhFile, nil
	}

	if _, err := os.Stat(ctx.AnkhFilePath); err == nil {
		ctx.Logger.Infof("Reading Ankh file %v", ctx.AnkhFilePath)
		ankhFile, err = ParseAnkhFile(ctx.AnkhFilePath)
		if err != nil {
			return ankhFile, err
		}
		ctx.Logger.Debugf("- OK: %v", ctx.AnkhFilePath)
	}

	// The single chart argument may have a version override in the format `name@version`
	// Extract that now if possible.
	versionOverride := ""
	tokens := strings.Split(singleChart, "@")
	if len(tokens) > 2 {
		ctx.Logger.Fatalf("Invalid chart '%v'. Too many `@` characters found. Chart must either be a name with no `@`, or in the combined `name@version` format", singleChart)
	}
	if len(tokens) == 2 {
		singleChart = tokens[0]
		versionOverride = tokens[1]
	}

	// If we find that our chart arg matches a chart in the array,
	// then that's the one and only chart we need to operate on.
	// Replace the charts array with that singleton, and return.
	for _, chart := range ankhFile.Charts {
		if singleChart == chart.Name {
			ctx.Logger.Debugf("Truncating Charts array to the singleton %v", singleChart)
			if versionOverride != "" {
				ctx.Logger.Infof("Using chart version %v and overriding any existing `path` config", versionOverride)
				newChart := chart
				newChart.Path = ""
				newChart.Version = versionOverride
				ankhFile.Charts = []Chart{newChart}
			} else {
				ankhFile.Charts = []Chart{chart}
			}
			return ankhFile, nil
		}
	}

	// The chart argument wasn't found in the charts array, so the user is attempting to operate
	// over an ad-hoc chart. If versionOverride is empty here, we'll prompt the user for a
	// valid version, and the choices will come from the helm registry.
	ankhFile = AnkhFile{
		Charts: []Chart{
			Chart{Version: versionOverride, Name: singleChart},
		},
	}
	ctx.Logger.Debugf("Returning ankhFile %+v", ankhFile)
	return ankhFile, nil
}
