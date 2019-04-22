package ankh

import (
	"io/ioutil"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

const minimalValidAnkhFileYAML string = `
dependencies: []
charts:
  - name: foo
    version: 0.0.0
`

var log = logrus.New()

func newValidAnkhConfig() AnkhConfig {
	return AnkhConfig{
		CurrentContextName: "test",
		Contexts: map[string]Context{
			"test": Context{
				EnvironmentClass: "dev",
				ResourceProfile:  "constrained",
				HelmRepositoryURL:  "http://localhost",
				KubeContext:      "dev",
			},
		},
	}
}

func TestAnkhConfigValidateAndInit(t *testing.T) {

	t.Run("valid AnkhConfig", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{Logger: log}, "")

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

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{Logger: log}, "secondcontext")

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

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{Logger: log}, "")

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

	t.Run("missing current context with explicit context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()
		ankhConfig.CurrentContextName = ""

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{Logger: log}, "test")
		if len(errs) > 0 {
			t.Logf("was expecting no errors, but got these `errs`: %+v", errs)
			t.Fail()
		}
	})

	t.Run("can't select a context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()
		ankhConfig.Contexts = map[string]Context{}

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{Logger: log}, "")

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

	t.Run("missing kube config in selected context", func(t *testing.T) {
		ankhConfig := newValidAnkhConfig()

		// "copy" the struct and reassign it since we can't modify a map in place
		context := ankhConfig.Contexts["test"]
		context.KubeContext = ""

		ankhConfig.Contexts["test"] = context

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{Logger: log}, "")

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
		context.EnvironmentClass = ""

		ankhConfig.Contexts["test"] = context

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{Logger: log}, "")

		hasCorrectError := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "missing or empty `environment-class`") {
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

		errs := ankhConfig.ValidateAndInit(&ExecutionContext{Logger: log}, "")

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
