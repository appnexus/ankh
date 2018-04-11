package ankh

import (
	"io/ioutil"
	"strings"
	"testing"
)

const minimalValidAnkhFileYAML string = `
admin-dependencies: []
dependencies: []
charts:
  - name: foo
    version: 0.0.0
`

const minimalValidAnkhConfigYAMLPath string = "testdata/testconfig.yaml"

func newValidAnkhConfig() AnkhConfig {
	return AnkhConfig{
		CurrentContextName:        "test",
		SupportedEnvironments:     []string{"dev"},
		SupportedResourceProfiles: []string{"constrained"},
		Contexts: map[string]Context{
			"test": Context{
				Environment:     "dev",
				ResourceProfile: "constrained",
				HelmRegistryURL: "http://localhost",
				KubeContext:     "dev",
			},
		},
	}
}

func TestAnkhConfigValidateAndInit(t *testing.T) {

	t.Run("valid AnkhConfig", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		if len(errs) > 0 {
			t.Logf("got errors when trying to validate an AnkhConfig: %v", errs)
			t.Fail()
		}
	})

	t.Run("valid AnkhConfig with two contexts and context override", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		secondContext := ankhConfig.Contexts["test"]
		secondContext.KubeContext = "secondkubecontext"
		ankhConfig.Contexts["secondcontext"] = secondContext

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{ContextOverride: "secondcontext"})

		if len(errs) > 0 {
			t.Logf("got errors when trying to validate an AnkhConfig: %v", errs)
			t.Fail()
		}

		if ankhConfig.CurrentContextName != "secondcontext" {
			t.Logf("did not get CurrentContextName equal to the override name 'secondcontext': got %v", ankhConfig.CurrentContextName)
			t.Fail()
		}

		if ankhConfig.CurrentContext.KubeContext != "secondkubecontext" {
			t.Logf("did not get CurrentContext with a KubeContext consistent with the override context 'secondkubecontext': got %v", ankhConfig.CurrentContext.KubeContext)
			t.Fail()
		}
	})

	t.Run("missing current context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()
		ankhConfig.CurrentContextName = ""

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "Missing or empty `current-context`") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})

	t.Run("missing current context with configOverride", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()
		ankhConfig.CurrentContextName = ""

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{ContextOverride: "test"})
		if len(errs) > 0 {
			t.Logf("was expecting no errors, but got these `errs`: %+v", errs)
			t.Fail()
		}
	})

	t.Run("missing supported-environments", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()
		ankhConfig.SupportedEnvironments = []string{}

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "Missing or empty `supported-environments`") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})

	t.Run("missing supported-resource-profiles", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()
		ankhConfig.SupportedResourceProfiles = []string{}

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "Missing or empty `supported-resource-profiles`") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})

	t.Run("cant select a context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()
		ankhConfig.Contexts = map[string]Context{}

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "Context 'test' not found in `contexts`") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})

	t.Run("bad environment in selected context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		// "copy" the struct and reassign it since we can't modify a map in place
		context := ankhConfig.Contexts["test"]
		context.Environment = "nope"

		ankhConfig.Contexts["test"] = context

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "Current context 'test' has environment 'nope': not found in `supported-environments` == [dev]") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})

	t.Run("bad resource-profile in selected context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		// "copy" the struct and reassign it since we can't modify a map in place
		context := ankhConfig.Contexts["test"]
		context.ResourceProfile = "nope"

		ankhConfig.Contexts["test"] = context

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "Current context 'test' has resource profile 'nope': not found in `supported-resource-profiles` == [constrained]") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})

	t.Run("missing helm registry url in selected context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		// "copy" the struct and reassign it since we can't modify a map in place
		context := ankhConfig.Contexts["test"]
		context.HelmRegistryURL = ""

		ankhConfig.Contexts["test"] = context

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "Current context 'test' has missing or empty `helm-registry-url`") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})

	t.Run("missing kube config in selected context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		// "copy" the struct and reassign it since we can't modify a map in place
		context := ankhConfig.Contexts["test"]
		context.KubeContext = ""

		ankhConfig.Contexts["test"] = context

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "Current context 'test' has missing or empty `kube-context`") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})

	t.Run("missing environment in selected context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		// "copy" the struct and reassign it since we can't modify a map in place
		context := ankhConfig.Contexts["test"]
		context.Environment = ""

		ankhConfig.Contexts["test"] = context

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "missing or empty `environment`") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})

	t.Run("missing resource profile in selected context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		// "copy" the struct and reassign it since we can't modify a map in place
		context := ankhConfig.Contexts["test"]
		context.ResourceProfile = ""

		ankhConfig.Contexts["test"] = context

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{})

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "missing or empty `resource-profile`") {
				hasCorrectError = true
			}
		}

		if !hasCorrectError {
			t.Logf("was expecting to find a specific error in `errs`: %v", errs)
			t.Fail()
		}
	})
}

func TestParseAnkhFile(t *testing.T) {
	t.Run("valid ankh file", func(t *testing.T) {
		file, err := ioutil.TempFile("", "")
		if err != nil {
			t.Log(err)
			t.Fail()
		}
		defer file.Close()

		file.WriteString(minimalValidAnkhFileYAML)

		_, err = ParseAnkhFile(file.Name())
		if err != nil {
			t.Log(err)
			t.Fail()
		}

	})

	t.Run("missing file", func(t *testing.T) {
		_, err := ParseAnkhFile("/does/not/exist")
		if err == nil {
			t.Log(err)
			t.Fail()
		}
	})

	t.Run("fail unmarshaling", func(t *testing.T) {
		file, err := ioutil.TempFile("", "")
		if err != nil {
			t.Log(err)
			t.Fail()
		}
		defer file.Close()

		file.WriteString(strings.TrimSpace(`
admin-typo-dependencies: []
dependencies: []
		`))

		_, err = ParseAnkhFile(file.Name())
		if err == nil {
			t.Log(err)
			t.Fail()
		}

	})

	t.Run("adds Path", func(t *testing.T) {
		file, err := ioutil.TempFile("", "")
		if err != nil {
			t.Log(err)
			t.Fail()
		}
		defer file.Close()

		file.WriteString(minimalValidAnkhFileYAML)

		ankhFile, err := ParseAnkhFile(file.Name())
		if err != nil {
			t.Log(err)
			t.Fail()
		}

		if ankhFile.Path != file.Name() {
			t.Logf("expected '%s' but got '%s'", ankhFile.Path, file.Name())
			t.Fail()
		}

	})

}

func TestGetAnkhConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		tmpDir, _ := ioutil.TempDir("", "")
		ctx := &ExecutionContext{
			AnkhConfigPath: minimalValidAnkhConfigYAMLPath,
			DataDir:        tmpDir,
		}

		_, err := GetAnkhConfig(ctx)
		if err != nil {
			t.Log(err)
			t.Fail()
		}
	})

	t.Run("missing config path", func(t *testing.T) {
		tmpDir, _ := ioutil.TempDir("", "")
		ctx := &ExecutionContext{
			AnkhConfigPath: "/does/not/exist",
			DataDir:        tmpDir,
		}

		_, err := GetAnkhConfig(ctx)
		if err == nil {
			t.Log("expected to find an error but didnt get one")
			t.Fail()
		}
	})

	t.Run("fail unmarshaling", func(t *testing.T) {
		tmpDir, _ := ioutil.TempDir("", "")
		tmpFile, _ := ioutil.TempFile("", "")

		tmpFile.WriteString(`invalid: config`)
		defer tmpFile.Close()

		ctx := &ExecutionContext{
			AnkhConfigPath: tmpFile.Name(),
			DataDir:        tmpDir,
		}

		_, err := GetAnkhConfig(ctx)
		if err == nil {
			t.Log("expected to find an error but didnt get one")
			t.Fail()
		}
	})
}
