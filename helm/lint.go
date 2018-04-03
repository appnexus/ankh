package helm

import (
	"fmt"
	"io"
	"strings"

	"github.com/appnexus/ankh/context"
	"gopkg.in/yaml.v2"
)

type Container struct {
	Name string
	Image string
}

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
			Spec struct {
				Containers []Container
			}
		}
	}
}

func LintObject(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, obj KubeObject) []error {
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
			e := fmt.Errorf("Deployment with name '%v': object's spec.template.metadata.labels is missing a `release` label with the release name as a value (in this case, '%v'). Found these labels on spec.template.metadata: %+v", obj.Kind, obj.Metadata.Name, release, obj.Spec.Template.Metadata.Labels)
			errors = append(errors, e)
		}
		ctx.Logger.Debugf("Deployment with name '%v': object spec.template.metadata.labels exists, and the release label is %v", obj.Kind, obj.Metadata.Name, obj.Spec.Template.Metadata.Labels["release"])
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

func PedanticLintObject(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, obj KubeObject) []error {
	errors := []error{}
	switch strings.ToLower(obj.Kind) {
	case "deployment":
		for _, c := range obj.Spec.Template.Spec.Containers {
			if !strings.HasPrefix(c.Image, "TODO REPO") {
				// TODO: stick this in a conf file
				e := fmt.Errorf("[Pedantic] Deployment with name '%v': has container '%v' with image '%v'. Deployment " +
					"image must point to 'TODO REPO'", obj.Metadata.Name, c.Name, c.Image)
				errors = append(errors, e)
			}
		}
	}
	return errors
}

func PedanticLint(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, objects []KubeObject) [] error {
	allErrors := []error{}
	ctx.Logger.Debugf("[Pedantic] Linting %v", ankhFile.Path)
	hasService := false

	for _, obj := range objects {
		if obj.Kind == "Service" {
			hasService = true
		}
		errors := PedanticLintObject(ctx, ankhFile, obj)
		if len(errors) > 0 {
			allErrors = append(allErrors, errors...)
		}
	}

	// Verify chart has a service object
	if !hasService {
		e := fmt.Errorf("Chart is missing a service object")
		allErrors = append(allErrors, e)
	}
	return allErrors
}

func Lint(ctx *ankh.ExecutionContext, helmOutput string, ankhFile ankh.AnkhFile) []error {
	decoder := yaml.NewDecoder(strings.NewReader(helmOutput))

	allErrors := []error{}
	objects := []KubeObject{}

	ctx.Logger.Debugf("Linting %v", ankhFile.Path)
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

		objects = append(objects, obj)

		errors := LintObject(ctx, ankhFile, obj)
		if len(errors) > 0 {
			allErrors = append(allErrors, errors...)
		}
	}

	if ctx.Pedantic {
		errors := PedanticLint(ctx, ankhFile, objects)
		if len(errors) > 0 {
			allErrors = append(allErrors, errors...)
		}
	}

	ctx.Logger.Debugf("Finished linting %v - found %v errors", ankhFile.Path, len(allErrors))
	return allErrors
}
