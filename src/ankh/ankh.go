package ankh

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"ankh/util"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// Captures all of the context required to execute a single iteration of Ankh
type ExecutionContext struct {
	AnkhConfig          AnkhConfig
	AnkhFilePath, Chart string

	Verbose, DryRun, Apply, UseContext bool

	AnkhConfigPath string
	KubeConfigPath string
	DataDir        string

	Logger *logrus.Logger
}

// Context is a struct that represents a context for applying files to a
// Kubernetes cluster
type Context struct {
	KubeContext     string `yaml:"kube-context"`
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

// ValidateAndInit ensures the AnkhConfig is internally sane and populates
// special fields if necessary.
func (ankhConfig *AnkhConfig) ValidateAndInit() []error {
	errors := []error{}

	if ankhConfig.CurrentContextName == "" {
		errors = append(errors, fmt.Errorf("missing or empty `current-context`"))
	}

	if ankhConfig.SupportedEnvironments == nil || len(ankhConfig.SupportedEnvironments) == 0 {
		errors = append(errors, fmt.Errorf("missing or empty `supported-environments`"))
	}

	if ankhConfig.SupportedResourceProfiles == nil || len(ankhConfig.SupportedResourceProfiles) == 0 {
		errors = append(errors, fmt.Errorf("missing or empty `supported-resource-profiles`"))
	}

	selectedContext, contextExists := ankhConfig.Contexts[ankhConfig.CurrentContextName]
	if contextExists == false {
		errors = append(errors, fmt.Errorf("context '%s' not found in `contexts`", ankhConfig.CurrentContextName))
	} else {
		if util.Contains(ankhConfig.SupportedEnvironments, selectedContext.Environment) == false {
			errors = append(errors, fmt.Errorf("environment '%s' not found in `supported-environments`", selectedContext.Environment))
		}

		if util.Contains(ankhConfig.SupportedResourceProfiles, selectedContext.ResourceProfile) == false {
			errors = append(errors, fmt.Errorf("resource profile '%s' not found in `supported-resource-profiles`", selectedContext.ResourceProfile))
		}

		if selectedContext.HelmRegistryURL == "" {
			errors = append(errors, fmt.Errorf("missing or empty `helm-registry-url`"))
		}

		if selectedContext.KubeContext == "" {
			errors = append(errors, fmt.Errorf("missing or empty `kube-context`"))
		}

		if selectedContext.Environment == "" {
			errors = append(errors, fmt.Errorf("missing or empty `environment`"))
		}

		if selectedContext.ResourceProfile == "" {
			errors = append(errors, fmt.Errorf("missing or empty `resource-profile`"))
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
		return config, fmt.Errorf("unable to process %s file: %v", filename, err)
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
		return ankhConfig, fmt.Errorf("unable to read ankh config '%s': %v", ctx.AnkhConfigPath, err)
	}

	if err := os.MkdirAll(ctx.DataDir, 0755); err != nil {
		return ankhConfig, fmt.Errorf("unable to make data dir '%s': %v", ctx.DataDir, err)
	}

	err = yaml.UnmarshalStrict(ankhRcFile, &ankhConfig)
	if err != nil {
		return ankhConfig, fmt.Errorf("unable to process ankh config '%s': %v", ctx.AnkhConfigPath, err)
	}

	errs := ankhConfig.ValidateAndInit()
	if len(errs) > 0 {
		return ankhConfig, fmt.Errorf("ankh config validation error(s):\n%s", util.MultiErrorFormat(errs))
	}

	return ankhConfig, nil
}
