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
	AnkhConfig   AnkhConfig
	AnkhFilePath string

	Verbose, DryRun, Apply, UseContext bool

	AnkhConfigPath string
	KubeConfigPath string
	DataDir        string

	Logger *logrus.Logger
}

// Context is a struct that represents a context for applying files to a
// Kubernetes cluster
type Context struct {
	KubeContext     string `yaml:"kube_context"`
	Environment     string `yaml:"environment"`
	Profile         string `yaml:"profile"`
	Release         string `yaml:"release"`
	HelmRegistryURL string `yaml:"helm_registry_url"`
	ClusterAdmin    bool   `yaml:"cluster_admin"`
	Global          map[string]interface{}
}

// AnkhConfig defines the shape of the ~/.ankh/config file used for global
// configuration options
type AnkhConfig struct {
	CurrentContextName string             `yaml:"current-context"` // note the intentionally offset names here
	CurrentContext     Context            `yaml:"-"`               // (private) filled in by code
	Contexts           map[string]Context `yaml:"contexts"`
}

// ValidateAndInit ensures the AnkhConfig is internally sane and populates
// special fields if necessary.
func (ankhConfig *AnkhConfig) ValidateAndInit() []error {
	errors := []error{}
	selectedContext, contextExists := ankhConfig.Contexts[ankhConfig.CurrentContextName]
	if contextExists == false {
		errors = append(errors, fmt.Errorf("context '%s' not found in `contexts`", ankhConfig.CurrentContextName))
	} else {
		ankhConfig.CurrentContext = selectedContext
	}
	return errors
}

type Chart struct {
	Name    string
	Version string
	// DefaultValues are values that apply regardless of environment
	DefaultValues    map[string]interface{} `yaml:"default_values"`
	Values           map[string]interface{}
	ResourceProfiles map[string]interface{} `yaml:"resource_profiles"`
	// Secrets is a temporary resting place for secrets, eventually we want to
	// load this from another secure source
	Secrets map[string]interface{}
}

// AnkhFile defines the shape of the `ankh.yaml` file which is used to define
// clusters and their contents
type AnkhFile struct {
	// (private) an absolute path to the ankh.yaml file
	Path string

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

	AdminDependencies []string `yaml:"admin_dependencies"`
	Dependencies      []string `yaml:"dependencies"`
}

func ParseAnkhFile(filename string) (AnkhFile, error) {
	config := AnkhFile{}
	ankhYaml, err := ioutil.ReadFile(fmt.Sprintf("%s", filename))
	if err != nil {
		return config, err
	}

	err = yaml.Unmarshal(ankhYaml, &config)
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

	err = yaml.Unmarshal(ankhRcFile, &ankhConfig)
	if err != nil {
		return ankhConfig, fmt.Errorf("unable to process ankh config '%s': %v", ctx.AnkhConfigPath, err)
	}

	errs := ankhConfig.ValidateAndInit()
	if len(errs) > 0 {
		return ankhConfig, fmt.Errorf("ankh config validation error(s):\n%s", util.MultiErrorFormat(errs))
	}

	return ankhConfig, nil
}
