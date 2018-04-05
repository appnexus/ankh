package helm

import (
	"fmt"
	"github.com/appnexus/ankh/context"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	appsv1beta1 "k8s.io/client-go/pkg/apis/apps/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	apiv1 "k8s.io/client-go/pkg/api/v1"
)

type Container struct {
	Name  string
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

func LintObjectMeta(ctx *ankh.ExecutionContext, meta v1.ObjectMeta, kind string) []error {
	errors := []error{}
	release := ctx.AnkhConfig.CurrentContext.Release
	if release == "" {
		return []error{}
	}

	// Verify that every object has a name with `-$release` as a suffix.
	suffix := fmt.Sprintf("-%v", release)
	if !strings.HasSuffix(meta.Name, suffix) {
		e := fmt.Errorf("Object with kind '%v' and name '%v': object name is missing a dashed release suffix "+
			"(in this case, '%v'). Use .Release.Name in your template to ensure that all objects are named with the release "+
			"as a suffix to aovid name collisions across releases.",
			kind, meta.Name, suffix)
		errors = append(errors, e)
	}
	ctx.Logger.Debugf("Object with kind '%v' and name '%v': object name does indeed contain the desired suffix `%v`",
		kind, meta.Name, suffix)

	// Verify that every object is labeled with a key `release` and value equal to the current context's release
	if meta.Labels["release"] != release {
		e := fmt.Errorf("Object with kind '%v' and name '%v': object is missing a `release` label with the "+
			"release name as a value (in this case, '%v'). Found these labels on the object: %+v",
			kind, meta.Name, release, meta.Labels)
		errors = append(errors, e)
	}
	ctx.Logger.Debugf("Object with kind '%v' and name '%v': object labels exist, and the release label is '%v'",
		kind, meta.Name, meta.Labels)

	return errors
}

func LintObject(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, obj runtime.Object) []error {
	errors := []error{}
	release := ctx.AnkhConfig.CurrentContext.Release

	switch obj.(type) {

	case *appsv1beta1.Deployment:
		deployment := obj.(*appsv1beta1.Deployment)

		metaErrors := LintObjectMeta(ctx, deployment.ObjectMeta, deployment.Kind)
		if len(metaErrors) > 0 {
			errors = append(errors, metaErrors...)
		}

		// The Deployment should create pods with the `release` label
		if deployment.Spec.Template.ObjectMeta.Labels["release"] != release {
			e := fmt.Errorf("Deployment with name '%v': object's spec.template.metadata.labels is missing a `release` " +
				"label with the release name as a value (in this case, '%v'). Found these labels on spec.template.metadata: %+v",
					deployment.Kind, deployment.ObjectMeta.Name, release, deployment.Spec.Template.ObjectMeta.Labels)
			errors = append(errors, e)
		}
		ctx.Logger.Debugf("Deployment with name '%v': object spec.template.metadata.labels exists, and the release " +
			"label is %v", deployment.Kind, deployment.ObjectMeta.Name, deployment.Spec.Template.ObjectMeta.Labels["release"])

	case *apiv1.Service:
		service := obj.(*apiv1.Service)

		metaErrors := LintObjectMeta(ctx, service.ObjectMeta, service.Kind)
		if len(metaErrors) > 0 {
			errors = append(errors, metaErrors...)
		}

			// If the Service is not targeting an ExternalName, it should target pods with a `release` label
			if service.Spec.Type != "ExternalName" {
				if service.Spec.Selector["release"] != release {
					e := fmt.Errorf("Service with type '%v' and name '%v': object's spec.selector is missing the `release` " +
						"key with the release name as a value (in this case, '%v'). Found these keys on spec.selector: %+v",
							service.Spec.Type, service.ObjectMeta.Name, release, service.Spec.Selector)
					errors = append(errors, e)
				}
				ctx.Logger.Debugf("Service with type '%v' and name '%v': object spec.selector exists, and the release " +
					"key is %v", service.Spec.Type, service.ObjectMeta.Name, service.Spec.Selector["release"])
			}
	}

	if ctx.Pedantic {
		pedanticErrors := PedanticLintObject(ctx, ankhFile, obj)
		if len(pedanticErrors) > 0 {
			errors = append(errors, pedanticErrors...)
		}
	}

	return errors
}

func PedanticLintObject(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, obj runtime.Object) []error {
	errors := []error{}
	switch obj.(type) {
	case *appsv1beta1.Deployment:
		deployment := obj.(*appsv1beta1.Deployment)
			for _, c := range deployment.Spec.Template.Spec.Containers {
				if !strings.HasPrefix(c.Image, "TODO REPO") {
					// TODO: stick this in a conf file
					e := fmt.Errorf("[Pedantic] Deployment with name '%v' has container '%v' with image '%v'. Deployment "+
						"image must point to 'TODO REPO'", deployment.ObjectMeta.Name, c.Name, c.Image)
					errors = append(errors, e)
					ctx.Logger.Debugf("Deployment with name '%v' has container '%v' with image '%v' correctly pointing to " +
						"'%v", deployment.ObjectMeta.Name, c.Name, c.Image, "TODO REPO")
				}
			}
		}

	return errors
}

func Lint(ctx *ankh.ExecutionContext, helmOutput string, ankhFile ankh.AnkhFile) []error {
	decode := scheme.Codecs.UniversalDeserializer().Decode
	parts := strings.Split(helmOutput, "---")

	allErrors := []error{}

	ctx.Logger.Debugf("Linting %v", ankhFile.Path)
	for _, part := range parts[1:] {
		obj, _, err := decode([]byte(part), nil, nil)

		if runtime.IsMissingKind(err) {
			ctx.Logger.Debugf("Skipping empty document")
			continue
		}

		ctx.Logger.Debugf("Decoded a kube object with kind '%v'", obj.GetObjectKind().GroupVersionKind().Kind)

		errors := LintObject(ctx, ankhFile, obj)
		if len(errors) > 0 {
			allErrors = append(allErrors, errors...)
		}
	}

	ctx.Logger.Debugf("Finished linting %v - found %v errors", ankhFile.Path, len(allErrors))
	return allErrors
}
