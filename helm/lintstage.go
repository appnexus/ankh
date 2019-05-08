package helm

import (
	"fmt"
	"io"
	"strings"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/plan"
	"gopkg.in/yaml.v2"
)

// TODO: Share this code with kubectl
type KubeObject struct {
	Kind     string
	Metadata struct {
		Name   string
		Labels map[string]string
	}
	Spec struct {
		Type     string
		Selector map[string]string
		Template struct {
			Metadata struct {
				Labels map[string]string
			}
		}
	}
}

type LintStage struct {
}

func NewLintStage() plan.Stage {
	return LintStage{}
}

func (stage LintStage) Execute(ctx *ankh.ExecutionContext, input *string, namespace string, wildCardLabels []string) (string, error) {
	if input == nil {
		panic("Cannot lint nil input")
	}

	errors := helmLint(ctx, *input)
	if len(errors) == 0 {
		return "", nil
	}

	for _, err := range errors {
		ctx.Logger.Warningf("%v", err)
	}
	return "", fmt.Errorf("Lint found %d errors", len(errors))
}

func lintObject(ctx *ankh.ExecutionContext, obj KubeObject) []error {
	release := ctx.AnkhConfig.CurrentContext.Release
	if release == "" {
		return []error{}
	}

	errors := []error{}

	// Verify that every object has a name with `-$release` as a suffix.
	suffix := fmt.Sprintf("-%v", release)
	if !strings.HasSuffix(obj.Metadata.Name, suffix) {
		e := fmt.Errorf("Object with kind '%v' and name '%v': object name is missing a dashed release suffix (in this case, '%v'). Use .Release.Name in your template to ensure that all objects are named with the release as a suffix to aovid name collisions across releases.",
			obj.Kind, obj.Metadata.Name, suffix)
		errors = append(errors, e)
	}
	ctx.Logger.Debugf("Object with kind '%v' and name '%v': object name does indeed contain the desired suffix `%v`", obj.Kind, obj.Metadata.Name, suffix)

	// Verify that every object is labeled with a key `release` and value equal to the current context's release
	if obj.Metadata.Labels["release"] != release {
		e := fmt.Errorf("Object with kind '%v' and name '%v': object is missing a `release` label with the release name as a value (in this case, '%v'). Found these labels on the object: %+v", obj.Kind, obj.Metadata.Name, release, obj.Metadata.Labels)
		errors = append(errors, e)
	}
	ctx.Logger.Debugf("Object with kind '%v' and name '%v': object labels exist, and the release label is '%v'", obj.Kind, obj.Metadata.Name, obj.Metadata.Labels["release"])

	switch strings.ToLower(obj.Kind) {
	case "deployment":
		// The Deployment should create pods with the `release` label
		if obj.Spec.Template.Metadata.Labels["release"] != release {
			e := fmt.Errorf("Deployment with name '%v': object's spec.template.metadata.labels is missing a `release` label with the release name as a value (in this case, '%v'). Found these labels on spec.template.metadata: %+v", obj.Metadata.Name, release, obj.Spec.Template.Metadata.Labels)
			errors = append(errors, e)
		}
		ctx.Logger.Debugf("Deployment with name '%v': object spec.template.metadata.labels exists, and the release label is %v", obj.Metadata.Name, obj.Spec.Template.Metadata.Labels["release"])
	case "service":
		// If the Service is not targeting an ExternalName, it should target pods with a `release` label
		if obj.Spec.Type != "ExternalName" {
			if obj.Spec.Selector["release"] != release {
				e := fmt.Errorf("Service with type '%v' and name '%v': object's spec.selector is missing the `release` key with the release name as a value (in this case, '%v'). Found these keys on spec.selector: %+v", obj.Spec.Type, obj.Metadata.Name, release, obj.Spec.Selector)
				errors = append(errors, e)
			}
			ctx.Logger.Debugf("Service with type '%v' and name '%v': object spec.selector exists, and the release key is %v", obj.Spec.Type, obj.Metadata.Name, obj.Spec.Selector["release"])
		}
	}

	return errors
}

func helmLint(ctx *ankh.ExecutionContext, helmOutput string) []error {
	decoder := yaml.NewDecoder(strings.NewReader(helmOutput))

	allErrors := []error{}
	for {
		obj := KubeObject{}
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}

		ctx.Logger.Debugf("Decoded a kube object with kind '%v'", obj.Kind)
		if obj.Kind == "" {
			// Ignore empty documents
			ctx.Logger.Debugf("Skipping empty document")
			continue
		}

		errors := lintObject(ctx, obj)
		if len(errors) > 0 {
			allErrors = append(allErrors, errors...)
		}
	}
	return allErrors
}
