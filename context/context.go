package ankh

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type Mode string

const (
	Apply    Mode = "apply"
	Rollback Mode = "rollback"
	Diff     Mode = "diff"
	Exec     Mode = "exec"
	Explain  Mode = "explain"
	Get      Mode = "get"
	Pods     Mode = "pods"
	Lint     Mode = "lint"
	Logs     Mode = "logs"
	Template Mode = "template"
)

// Captures all of the context required to execute a single iteration of Ankh
type ExecutionContext struct {
	AnkhConfig, OriginalAnkhConfig AnkhConfig

	AnkhFilePath string
	// Overrides:
	// Chart may be a single chart in the charts array, or a local chart path
	// Namespace may override a value present in the AnkhFile
	Chart string
	Namespace *string

	Mode Mode

	Verbose, Quiet, CatchSignals, DryRun, Describe, WarnOnConfigError, UseContext, IgnoreContextAndEnv, IgnoreConfigErrors bool

	AnkhConfigPath string
	KubeConfigPath string
	Context        string
	Release        string
	Environment    string
	DataDir        string
	HelmSetValues  map[string]string

	Filters []string

	ExtraArgs, PassThroughArgs []string

	HelmVersion, KubectlVersion string

	Logger *logrus.Logger
}

// Context is a struct that represents a context for applying files to a
// Kubernetes cluster
type Context struct {
	Source             string                 `yaml:"-"` // private field. specifies which config file declared this.
	KubeContext        string                 `yaml:"kube-context,omitempty"`
	KubeServer         string                 `yaml:"kube-server,omitempty"`
	Environment        string                 `yaml:"environment,omitempty"` // deprecated in favor of `environment-class`
	EnvironmentClass   string                 `yaml:"environment-class"`     // omitempty until we remove `environment`
	ResourceProfile    string                 `yaml:"resource-profile"`
	Release            string                 `yaml:"release,omitempty"`
	HelmRegistryURL    string                 `yaml:"helm-registry-url,omitempty"` // deprecated in favor of top-level config `helm.registry`
	ClusterAdminUnused bool                   `yaml:"cluster-admin,omitempty"`     // deprecated
	Global             map[string]interface{} `yaml:"global",omitempty"`
}

// An Environment is a collection of contexts over which operations should be applied
type Environment struct {
	Source   string   `yaml:"-"` // private field. specifies which config file declared this.
	Contexts []string `yaml:"contexts"`
}

type KubectlConfig struct {
	WildCardLabels []string `yaml:"wildCardLabels,omitempty"`
}

type HelmConfig struct {
	TagValueName string `yaml:"tagValueName"`
	Registry     string `yaml:"registry"`
	AuthType     string `yaml:"authType"`
}

type DockerConfig struct {
	Registry string `yaml:"registry"`
}

// AnkhConfig defines the shape of the ~/.ankh/config file used for global
// configuration options
type AnkhConfig struct {
	Include                           []string               `yaml:"include"`
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
	CurrentContextUnused string        `yaml:"current-context"` // transitionary: this should never be user-supplied
	CurrentContext       string        `yaml:"-"`               // transitionary: this should never be user-supplied
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
			ctx.Logger.Infof("Using release argument \"%v\" from command line for context \"%v\".", ctx.Release, ankhConfig.CurrentContextName)
		}
		ankhConfig.CurrentContext.Release = ctx.Release
	}
	return errors
}

// TODO: Rename me to target?
type Chart struct {
	Path         string
	Name         string // TODO: Merge me and version into `Chart`?
	Version      string // TODO: Merge me and Name into `Chart`?
	Tag          string
	TagValueName string
	Namespace    *string
	// DefaultValues are values that apply unconditionally, with lower precedence than values supplied in the fields below.
	DefaultValues map[string]interface{} `yaml:"default-values"`
	// Values, by environment-class, resource-profile, or release. MapSlice preserves map ordering so we can regex search from top to bottom.
	Values           yaml.MapSlice
	ResourceProfiles yaml.MapSlice `yaml:"resource-profiles"`
	Releases         yaml.MapSlice
}

type ChartFiles struct {
	Dir                      string
	ChartDir                 string
	GlobalPath               string
	ValuesPath               string
	AnkhValuesPath           string
	AnkhResourceProfilesPath string
	AnkhReleasesPath         string
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

	err = yaml.UnmarshalStrict(body, &ankhFile)
	if err != nil {
		return ankhFile, fmt.Errorf("Error loading Ankh file '%v': %v\nAll Ankh yamls are parsed strictly. Please refer to README.md for the correct schema of an Ankh file", ankhFilePath, err)
	}

	return ankhFile, nil
}

func GetAnkhFile(ctx *ExecutionContext) (AnkhFile, error) {
	if ctx.Chart == "" {
		ctx.Logger.Infof("Reading Ankh file %v", ctx.AnkhFilePath)
		ankhFile, err := ParseAnkhFile(ctx.AnkhFilePath)
		if err == nil {
			ctx.Logger.Debugf("- OK: %v", ctx.AnkhFilePath)
		}
		return ankhFile, err
	}

	// We have a chart argument, which makes things more complicated.
	return getAnkhFileForChart(ctx, ctx.Chart)
}

func getAnkhFileForChart(ctx *ExecutionContext, singleChart string) (AnkhFile, error) {
	versionOverride := ""

	var ankhFile AnkhFile
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
	tokens := strings.Split(singleChart, "@")
	if len(tokens) > 2 {
		ctx.Logger.Fatalf("Invalid chart '%v'. Too many `@` characters found. Chart must either be a name with no `@`, or in the combined `name@version` format.")
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
