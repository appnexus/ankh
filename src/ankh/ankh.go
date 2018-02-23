package ankh

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"ankh/util"
	"crypto/tls"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"net/http"
	"strings"
	"time"
)

// Captures all of the context required to execute a single iteration of Ankh
type ExecutionContext struct {
	AnkhConfig          AnkhConfig
	AnkhFilePath, Chart string

	Verbose, DryRun, Apply, Explain, UseContext bool

	AnkhConfigPath string
	KubeConfigPath string
	DataDir        string
	HelmSetValues  map[string]string

	HelmVersion, KubectlVersion string

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

type ChartFiles struct {
	Dir                      string
	ChartDir                 string
	GlobalPath               string
	ValuesPath               string
	AnkhValuesPath           string
	AnkhResourceProfilesPath string
}

func CreateReducedYAMLFile(filename, key string) ([]byte, error) {
	in := make(map[string]interface{})
	var result []byte
	inBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return result, err
	}

	if err = yaml.UnmarshalStrict(inBytes, &in); err != nil {
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

func GetChartFileContent(ctx *ExecutionContext, path string, useContext bool, key string) ([]byte, error) {
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

func FindChartFiles(ctx *ExecutionContext, ankhFile AnkhFile, chart Chart) (ChartFiles, error) {
	name := chart.Name
	version := chart.Version

	dirPath := filepath.Join(filepath.Dir(ankhFile.Path), "charts", name)
	_, dirErr := os.Stat(dirPath)

	files := ChartFiles{}
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
	files = ChartFiles{
		Dir:                      tmpDir,
		ChartDir:                 chartDir,
		GlobalPath:               filepath.Join(tmpDir, "global.yaml"),
		ValuesPath:               filepath.Join(chartDir, "values.yaml"),
		AnkhValuesPath:           filepath.Join(chartDir, "ankh-values.yaml"),
		AnkhResourceProfilesPath: filepath.Join(chartDir, "ankh-resource-profiles.yaml"),
	}

	return files, nil
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
