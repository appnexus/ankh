package ankh

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"github.com/appnexus/ankh/util"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type Mode string

const (
	Apply    Mode = "apply"
	Explain  Mode = "explain"
	Lint     Mode = "lint"
	Template Mode = "template"
)

// Captures all of the context required to execute a single iteration of Ankh
type ExecutionContext struct {
	AnkhConfig          AnkhConfig
	AnkhFilePath, Chart string

	Mode Mode

	Verbose, DryRun, UseContext, WarnOnConfigError, IgnoreConfigError bool

	AnkhConfigPath  string
	KubeConfigPath  string
	ContextOverride string
	DataDir         string
	HelmSetValues   map[string]string

	Filters []string

	HelmVersion, KubectlVersion string

	Logger *logrus.Logger
}

// Context is a struct that represents a context for applying files to a
// Kubernetes cluster
type Context struct {
	KubeContext     string `yaml:"kube-context"`
	KubeServer      string `yaml:"kube-server"`
	Environment     string `yaml:"environment"`
	ResourceProfile string `yaml:"resource-profile"`
	Release         string `yaml:"release"`
	HelmRegistryURL string `yaml:"helm-registry-url"`
	ClusterAdmin    bool   `yaml:"cluster-admin"`
	Global          map[string]interface{}
}

// AnkhConfig defines the shape of the ~/.ankh/config file used for global
// configuration options
type AnkhConfig struct {
	SupportedEnvironments     []string           `yaml:"supported-environments"`
	SupportedResourceProfiles []string           `yaml:"supported-resource-profiles"`
	CurrentContextName        string             `yaml:"current-context"`
	CurrentContext            Context            `yaml:"-"` // private, filled in by init code. The `-` instructs the yaml lib to not look for this field
	Contexts                  map[string]Context `yaml:"contexts"`
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
	ApiVersion     string        `yaml:"apiVersion"`
	Kind           string        `yaml:"kind"`
	Clusters       []KubeCluster `yaml:"clusters"`
	Contexts       []KubeContext `yaml:"contexts"`
	CurrentContext string        `yaml:"current-context"`
}

// ValidateAndInit ensures the AnkhConfig is internally sane and populates
// special fields if necessary.
func (ankhConfig *AnkhConfig) ValidateAndInit(ctx *ExecutionContext) []error {
	errors := []error{}

	if ctx.ContextOverride != "" {
		ankhConfig.CurrentContextName = ctx.ContextOverride
	}

	if ankhConfig.CurrentContextName == "" {
		errors = append(errors, fmt.Errorf("Missing or empty `current-context`"))
	}

	if ankhConfig.SupportedEnvironments == nil || len(ankhConfig.SupportedEnvironments) == 0 {
		errors = append(errors, fmt.Errorf("Missing or empty `supported-environments`"))
	}

	if ankhConfig.SupportedResourceProfiles == nil || len(ankhConfig.SupportedResourceProfiles) == 0 {
		errors = append(errors, fmt.Errorf("Missing or empty `supported-resource-profiles`"))
	}

	selectedContext, contextExists := ankhConfig.Contexts[ankhConfig.CurrentContextName]
	if contextExists == false {
		errors = append(errors, fmt.Errorf("Context '%s' not found in `contexts`", ankhConfig.CurrentContextName))
	} else {
		if util.Contains(ankhConfig.SupportedEnvironments, selectedContext.Environment) == false {
			errors = append(errors, fmt.Errorf("Current context '%s' has environment '%s': not found in `supported-environments` == %v", ankhConfig.CurrentContextName, selectedContext.Environment, ankhConfig.SupportedEnvironments))
		}

		if util.Contains(ankhConfig.SupportedResourceProfiles, selectedContext.ResourceProfile) == false {
			errors = append(errors, fmt.Errorf("Current context '%s' has resource profile '%s': not found in `supported-resource-profiles` == %v", ankhConfig.CurrentContextName, selectedContext.ResourceProfile, ankhConfig.SupportedResourceProfiles))
		}

		if selectedContext.HelmRegistryURL == "" {
			errors = append(errors, fmt.Errorf("Current context '%s' has missing or empty `helm-registry-url`", ankhConfig.CurrentContextName))
		}

		if selectedContext.KubeContext == "" && selectedContext.KubeServer == "" {
			errors = append(errors, fmt.Errorf("Current context '%s' has missing or empty `kube-context` or `kube-server`", ankhConfig.CurrentContextName))
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
				ApiVersion:     "v1",
				Kind:           "Config",
				Clusters:       []KubeCluster{kubeCluster},
				Contexts:       []KubeContext{kubeContext},
				CurrentContext: kubeContext.Name,
			}

			kubeConfigPath := path.Join(ctx.DataDir, "kubeconfig.yaml")
			kubeConfigBytes, err := yaml.Marshal(kubeConfig)
			if err != nil {
				return []error{err}
			}

			if err := ioutil.WriteFile(kubeConfigPath, kubeConfigBytes, 0644); err != nil {
				return []error{err}
			}

			selectedContext.KubeContext = kubeContext.Name
			ctx.KubeConfigPath = kubeConfigPath
		}

		if selectedContext.Environment == "" {
			errors = append(errors, fmt.Errorf("Current context '%s' has missing or empty `environment`", ankhConfig.CurrentContextName))
		}

		if selectedContext.ResourceProfile == "" {
			errors = append(errors, fmt.Errorf("Current context '%s' has missing or empty `resource-profile`", ankhConfig.CurrentContextName))
		}
		ankhConfig.CurrentContext = selectedContext
	}
	return errors
}

type Chart struct {
	Name    string
	Version string
	// DefaultValues are values that apply regardless of environment
	DefaultValues    map[string]interface{} `yaml:"default-values"`
	Values           map[string]interface{}
	ResourceProfiles map[string]interface{} `yaml:"resource-profiles"`
	// Secrets is a temporary resting place for secrets, eventually we want to
	// load this from another secure source
	Secrets map[string]interface{}
}

type ChartFiles struct {
	Dir                      string
	ChartDir                 string
	GlobalPath               string
	ValuesPath               string
	AnkhValuesPath           string
	AnkhResourceProfilesPath string
}

// AnkhFile defines the shape of the `ankh.yaml` file which is used to define
// clusters and their contents
type AnkhFile struct {
	// (private) an absolute path to the ankh.yaml file
	Path string `yaml:"-"`

	Bootstrap struct {
		Scripts []struct {
			Path string
		}
	}

	Teardown struct {
		Scripts []struct {
			Path string
		}
	}

	// Nested children. This is usually populated by looking at the
	// `ChildrenPaths` property and finding the child definitions

	// The Kubernetes namespace to apply to
	Namespace string

	Charts []Chart

	AdminDependencies []string `yaml:"admin-dependencies"`
	Dependencies      []string `yaml:"dependencies"`
}

func ParseAnkhFile(filename string) (AnkhFile, error) {
	config := AnkhFile{}
	ankhYaml, err := ioutil.ReadFile(fmt.Sprintf("%s", filename))
	if err != nil {
		return config, err
	}

	err = yaml.UnmarshalStrict(ankhYaml, &config)
	if err != nil {
		return config, fmt.Errorf("error loading ankh file '%v': %v\nAll Ankh yamls are parsed strictly. Please refer to README.md for the correct schema of an Ankh file", filename, err)
	}

	// Add the absolute path of the config to the struct
	config.Path, err = filepath.Abs(filename)
	if err != nil {
		return config, err
	}

	return config, nil
}

func GetAnkhConfig(ctx *ExecutionContext) (AnkhConfig, error) {
	ankhConfig := AnkhConfig{}

	ankhRcFile, err := ioutil.ReadFile(ctx.AnkhConfigPath)
	if err != nil {
		return ankhConfig, fmt.Errorf("Unable to read ankh config '%s': %v", ctx.AnkhConfigPath, err)
	}

	if err := os.MkdirAll(ctx.DataDir, 0755); err != nil {
		return ankhConfig, fmt.Errorf("Unable to make data dir '%s': %v", ctx.DataDir, err)
	}

	err = yaml.UnmarshalStrict(ankhRcFile, &ankhConfig)
	if err != nil {
		return ankhConfig, fmt.Errorf("Error loading ankh config '%s': %v", ctx.AnkhConfigPath, err)
	}

	errs := ankhConfig.ValidateAndInit(ctx)
	if len(errs) > 0 {
		return ankhConfig, fmt.Errorf("Error(s) validating ankh config '%s':\n%s", ctx.AnkhConfigPath, util.MultiErrorFormat(errs))
	}

	return ankhConfig, nil
}
