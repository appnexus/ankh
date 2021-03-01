package main

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/docker"
	"github.com/appnexus/ankh/helm"
	"github.com/appnexus/ankh/jira"
	"github.com/appnexus/ankh/kubectl"
	"github.com/appnexus/ankh/plan"
	"github.com/appnexus/ankh/slack"
	"github.com/appnexus/ankh/util"
	"github.com/imdario/mergo"
)

func printEnvironments(ankhConfig *ankh.AnkhConfig) {
	keys := []string{}
	for k, _ := range ankhConfig.Environments {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		log.Infof("* %v", name)
	}
}

func getEnvironmentTable(ankhConfig *ankh.AnkhConfig) []string {
	buf := bytes.NewBufferString("")
	w := tabwriter.NewWriter(buf, 0, 8, 8, ' ', 0)
	fmt.Fprintf(w, "NAME\tCONTEXTS\tSOURCE\n")
	keys := []string{}
	for k, _ := range ankhConfig.Environments {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		env, _ := ankhConfig.Environments[name]
		fmt.Fprintf(w, "%v\t%v\t%v\n", name, strings.Join(env.Contexts, ","), env.Source)
	}
	w.Flush()
	return strings.Split(buf.String(), "\n")
}

func getContextTable(ankhConfig *ankh.AnkhConfig) []string {
	buf := bytes.NewBufferString("")
	w := tabwriter.NewWriter(buf, 0, 8, 8, ' ', 0)
	fmt.Fprintf(w, "NAME\tRELEASE\tENVIRONMENT-CLASS\tRESOURCE-PROFILE\tKUBE-CONTEXT/SERVER\tSOURCE\n")
	keys := []string{}
	for k, _ := range ankhConfig.Contexts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		ctx, _ := ankhConfig.Contexts[name]
		target := ctx.KubeContext
		if target == "" {
			target = ctx.KubeServer
		}
		fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v\n", name, ctx.Release, ctx.EnvironmentClass, ctx.ResourceProfile, target, ctx.Source)
	}
	w.Flush()
	return strings.Split(buf.String(), "\n")
}

func printContexts(ankhConfig *ankh.AnkhConfig) {
	keys := []string{}
	for k, _ := range ankhConfig.Contexts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		log.Infof("* %v", name)
	}
}

func reconcileMissingConfigs(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile) error {
	// Make sure that we don't use the tag argument for more than one Chart.
	// When this happens, it is almost always an error, because a tag value
	// is typically only valid/intended for a single chart.
	tagArgumentUsedForChart := ""

	// Prompt for chart versions if any are missing
	for i := 0; i < len(ankhFile.Charts); i++ {
		chart := &ankhFile.Charts[i]

		if chart.Path == "" && chart.Version == "" {
			ctx.Logger.Infof("Found chart \"%v\" without a version", chart.Name)
			if ctx.NoPrompt {
				ctx.Logger.Fatalf("Chart \"%v\" missing version (and no 'path' set either, not prompting due to --no-prompt)",
					chart.Name)
			}

			repository := ctx.DetermineHelmRepository(&chart.HelmRepository)
			versions, err := helm.ListVersions(ctx, repository, chart.Name, true)
			if err != nil {
				return err
			}

			versionsList := util.FilterStringsContaining(strings.Split(strings.Trim(versions, "\n "), "\n"), ctx.ChartVersionFilter)

			selectedVersion, err := util.PromptForSelection(versionsList,
				fmt.Sprintf("Select a version for chart \"%v\"", chart.Name), false)
			if err != nil {
				return err
			}

			chart.Version = selectedVersion
			ctx.Logger.Infof("Using chart \"%v\" at version \"%v\" based on prompt selection", chart.Name, chart.Version)
		} else if chart.Path != "" {
			ctx.Logger.Infof("Using chart \"%v\" from local path \"%v\"", chart.Name, chart.Path)
		}

		// Now that we have either a version or a local path, fetch the chart metadata and merge it.
		repository := ctx.DetermineHelmRepository(&chart.HelmRepository)
		meta, err := helm.FetchChartMeta(ctx, repository, chart)
		if err != nil {
			return fmt.Errorf("Error fetching chart \"%v\": %v", chart.Name, err)
		}
		mergo.Merge(&chart.ChartMeta, meta)

		// If namespace is set on the command line, we'll use that as an
		// override later during executeChartsOnNamespace, so don't check
		// for anything here.
		// - command line override, ankh file, chart meta.
		if ctx.Namespace == nil {
			if ankhFile.Namespace != nil {
				extraLog := ""
				if chart.ChartMeta.Namespace != nil && *ankhFile.Namespace != *chart.ChartMeta.Namespace {
					extraLog = fmt.Sprintf(" (overriding namespace \"%v\" from ankh.yaml present in the chart)",
						*chart.ChartMeta.Namespace)
				}
				ctx.Logger.Warnf("Using namespace \"%v\" from Ankh file for chart \"%v\"%v. This feature will be removed in Ankh 2.0",
					*ankhFile.Namespace, chart.Name, extraLog)
				chart.ChartMeta.Namespace = ankhFile.Namespace
			} else if chart.ChartMeta.Namespace == nil {
				ctx.Logger.Infof("Found chart \"%v\" without a namespace", chart.Name)
				if ctx.NoPrompt {
					ctx.Logger.Fatalf("Chart \"%v\" missing namespace (not prompting due to --no-prompt)", chart.Name)
				}
				if len(ctx.AnkhConfig.Namespaces) > 0 {
					selectedNamespace, err := util.PromptForSelection(ctx.AnkhConfig.Namespaces,
						fmt.Sprintf("Select a namespace for chart '%v' (or re-run with -n/--namespace to provide your own)",
							chart.Name), false)
					if err != nil {
						return err
					}
					chart.ChartMeta.Namespace = &selectedNamespace
				} else {
					providedNamespace, err := util.PromptForInput("",
						fmt.Sprintf("Provide a namespace for chart '%v' (or enter nothing to denote no explicit namespace) > ",
							chart.Name))
					if err != nil {
						return err
					}
					chart.ChartMeta.Namespace = &providedNamespace

				}
				ctx.Logger.Infof("Using namespace \"%v\" for chart \"%v\" based on prompt selection",
					*chart.ChartMeta.Namespace, chart.Name)
			} else {
				ctx.Logger.Infof("Using namespace \"%v\" for chart \"%v\" based on ankh.yaml present in the chart",
					*chart.ChartMeta.Namespace, chart.Name)
			}
		}

		// tagKey comes directly from the chart's metadata
		tagKey := chart.ChartMeta.TagKey

		// Do nothing if tagKey is not configured - the user does not want this behavior.
		if tagKey == "" {
			if ctx.Tag != nil {
				ctx.Logger.Fatalf("Tag has been provided but `tagKey` is not configured on either the `chart` in an AnkhFile, nor in an `ankh.yaml` inside the helm chart. " +
					"This means you passed a tag value, but have not told Ankh which helm value corresponds " +
					"to the tag value/variable in your helm chart. Tag is shorthand for `--set $tagKey=$tag`, " +
					"so you can use that instead, or you can ensure that `chart.tagKey` is configured")
			}
			continue
		} else {
			ctx.Logger.Infof("Using tagKey \"%v\" for chart \"%v\" based on ankh.yaml present in the chart", chart.ChartMeta.TagKey, chart.Name)
		}

		if ctx.Tag != nil {
			if tagArgumentUsedForChart != "" {
				complaint := fmt.Sprintf("Cannot use tag value for chart \"%v\" because it was already used for chart \"%v\". "+
					"A tag value is almost always intended for use with a single chart. To ignore this error and "+
					"use tag value \"%v\" for _all_ charts, re-un using `ankh --ignore-config-errors ...` ",
					chart.Name, tagArgumentUsedForChart, *ctx.Tag)
				if ctx.IgnoreConfigErrors {
					ctx.Logger.Warnf(complaint)
				} else {
					ctx.Logger.Fatalf(complaint)
				}
			}

			ctx.Logger.Infof("Using tag value \"%v=%s\" based on --tag argument", tagKey, *ctx.Tag)
			chart.Tag = ctx.Tag
			tagArgumentUsedForChart = chart.Name
			continue
		}

		// Treat any existing --set tagKey=$tag argument as authoritative
		for k, v := range ctx.HelmSetValues {
			if k == tagKey {
				ctx.Logger.Infof("Using tag value \"%v=%s\" based on --set argument", tagKey, v)
				t := v
				chart.Tag = &t
				break
			}
		}

		// Treat any existing `tag` in `default-values` for this chart as the next-most authoritative
		for k, v := range chart.DefaultValues {
			if k == tagKey {
				ctx.Logger.Infof("Using tag value \"%v=%s\" based on default-values present in the Ankh file", tagKey, v)
				t, ok := v.(string)
				if !ok {
					ctx.Logger.Fatalf("Could not use value '%+v' from default-values in chart %v "+
						"as a string value for tagKey '%v'", v, chart.Name, tagKey)
				}
				chart.Tag = &t
				break
			}
		}

		// For certain operations, we can assume a safe `unset` value for tagKey
		// for the sole purpose of templating the Helm chart. The value won't be used
		// meaningfully (like it would be with apply), so we choose this method instead
		// of prompting the user for a value that isn't meaningful.
		switch ctx.Mode {
		case ankh.Explain:
			fallthrough
		case ankh.Rollback:
			fallthrough
		case ankh.Get:
			fallthrough
		case ankh.Pods:
			fallthrough
		case ankh.Exec:
			fallthrough
		case ankh.Logs:
			if chart.Tag != nil {
				break
			}

			_, ok := ctx.HelmSetValues[tagKey]
			if !ok {
				// It's unset, so set it for the purpose of this execution
				tag := "__ankh_tag_value_unset___"
				ctx.Logger.Debugf("Setting configured tagKey %v=%v for a safe operation",
					tagKey, tag)
				chart.Tag = &tag
			}
		}

		// If we stil don't have a chart.Tag value, prompt.
		if chart.Tag == nil {
			if ctx.NoPrompt {
				ctx.Logger.Fatalf("Chart \"%v\" missing value for `tagKey` (configured to be '%v',  not prompting due to --no-prompt)",
					tagKey, chart.Name)
			}

			registryDomain := ctx.AnkhConfig.Docker.Registry
			image := ""
			if chart.ChartMeta.TagImage != "" {
				// No need to prompt for an image name if we already have one in the chart metdata
				registryDomain, image, err = docker.ParseImage(ctx, chart.ChartMeta.TagImage)
				check(err)

				ctx.Logger.Infof("Using tagImage \"%v\" for chart \"%v\" based on ankh.yaml present in the chart", chart.ChartMeta.TagImage, chart.Name)
				ctx.Logger.Debugf("Parsed tagImage into registryDomain '%v' and image '%v'", registryDomain, image)
			} else {
				ctx.Logger.Infof("Found chart \"%v\" without a value for \"%v\" ", chart.Name, tagKey)
				if ctx.AnkhConfig.Docker.Registry == "" {
					ctx.Logger.Fatalf("Cannot prompt for an image tag, no Docker registry configured.")
				}
				defaultValue := chart.Name
				image, err = util.PromptForInput(defaultValue,
					fmt.Sprintf("No tag specified for chart '%v'. Provide the name of an image in registry '%v' to select a tag for, "+
						"or nothing to skip this step > ", ctx.AnkhConfig.Docker.Registry, chart.Name))
				check(err)
			}

			if image == "" {
				ctx.Logger.Infof("Skipping tag prompt since no image name was provided")
				continue
			}

			if ctx.AnkhConfig.Docker.Registry == "" {
				ctx.Logger.Fatalf("Cannot prompt for an image tag, no Docker registry configured.")
			}

			output, err := docker.ListTags(ctx, registryDomain, image, true)
			check(err)

			trimmedOutput := strings.Trim(output, "\n ")
			if trimmedOutput != "" {
				tags := strings.Split(trimmedOutput, "\n")
				tag, err := util.PromptForSelection(tags, fmt.Sprintf("Select a value for \"%v\"", tagKey), false)
				check(err)

				ctx.Logger.Infof("Using implicit \"--set tag %v=%s\" based on prompt selection", tagKey, tag)
				chart.Tag = &tag
			} else if image != "" {
				complaint := fmt.Sprintf("Chart \"%v\" missing value for `tagKey` (configured to be `%v`). "+
					"You may want to try passing a tag value explicitly using `ankh --set %v=... `, or simply ignore "+
					"this error entirely using `ankh --ignore-config-errors ...` (not recommended)",
					chart.Name, tagKey, tagKey)
				if ctx.IgnoreConfigErrors {
					ctx.Logger.Warnf("%v", complaint)
				} else {
					ctx.Logger.Fatalf("%v", complaint)
				}
			}
		}

		// we should finally have a tag value
		ctx.DeploymentTag = *chart.Tag

	}

	return nil
}

func logExecuteAnkhFile(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile) {
	action := ""
	switch ctx.Mode {
	case ankh.Apply:
		action = "Applying chart"
	case ankh.Explain:
		action = "Explaining chart"
	case ankh.Deploy:
		action = "Running multi-stage deployment on chart"
	case ankh.Rollback:
		action = "Rolling back pods associated with chart"
	case ankh.Diff:
		action = "Diffing objects from chart"
	case ankh.Exec:
		action = "Executing on pods from chart"
	case ankh.Get:
		action = "Getting objects from chart"
	case ankh.Pods:
		action = "Getting pods associated with chart"
	case ankh.Template:
		action = "Templating"
	case ankh.Lint:
		action = "Linting"
	case ankh.Logs:
		action = "Getting logs for pods from chart"
	}

	releaseLog := ""
	if ctx.AnkhConfig.CurrentContext.Release != "" {
		releaseLog = fmt.Sprintf(" release \"%v\"", ctx.AnkhConfig.CurrentContext.Release)
	}

	dryLog := ""
	if ctx.DryRun {
		dryLog = " (dry run)"
	} else if ctx.Mode == ankh.Explain {
		dryLog = " (explaining)"
	}

	contextLog := fmt.Sprintf(" using kube-context \"%v\"", ctx.AnkhConfig.CurrentContext.KubeContext)
	if ctx.AnkhConfig.CurrentContext.KubeServer != "" {
		contextLog = fmt.Sprintf(" to kube-server \"%v\"", ctx.AnkhConfig.CurrentContext.KubeServer)
	}

	ctx.Logger.Infof("%v%v%v%v with environment class \"%v\" and resource profile \"%v\"", action,
		releaseLog, dryLog, contextLog,
		ctx.AnkhConfig.CurrentContext.EnvironmentClass,
		ctx.AnkhConfig.CurrentContext.ResourceProfile)
}

func execute(ctx *ankh.ExecutionContext) {
	rootAnkhFile, err := ankh.GetAnkhFile(ctx)
	check(err)

	contexts := []string{}
	if ctx.Environment != "" {
		environment, ok := ctx.AnkhConfig.Environments[ctx.Environment]
		if !ok {
			log.Errorf("Environment '%v' not found in `environments`", ctx.Environment)
			log.Info("The following environments are available:")
			printEnvironments(&ctx.AnkhConfig)
			os.Exit(1)
		}

		contexts = environment.Contexts
	}

	if len(contexts) > 0 {
		log.Infof("Executing over environment \"%v\" with contexts [ %v ]", ctx.Environment, strings.Join(contexts, ", "))

		for _, context := range contexts {
			log.Infof("Beginning to operate on context \"%v\" in environment \"%v\"", context, ctx.Environment)
			switchContext(ctx, &ctx.AnkhConfig, context)
			executeContext(ctx, &rootAnkhFile)
			log.Infof("Finished with context \"%v\" in environment \"%v\"", context, ctx.Environment)
		}
	} else {
		executeContext(ctx, &rootAnkhFile)
	}

	if ctx.SlackChannel != "" {
		if err := slack.PingSlackChannel(ctx, &rootAnkhFile); err != nil {
			ctx.Logger.Errorf("Slack message failed with error: %v", err)
		}
	}

	if ctx.CreateJiraTicket {
		if err := jira.CreateJiraTicket(ctx, &rootAnkhFile); err != nil {
			ctx.Logger.Errorf("Unable to create JIRA ticket. %v", err)
		}
	}
}

func executeChartsOnNamespace(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile, charts []ankh.Chart, namespace string) {
	// Only pass wildcard labels for "get"-oriented operations.
	useWildCardLabels := false
	switch ctx.Mode {
	case ankh.Diff:
		fallthrough
	case ankh.Get:
		fallthrough
	case ankh.Pods:
		fallthrough
	case ankh.Exec:
		fallthrough
	case ankh.Logs:
		useWildCardLabels = true
	}

	if ctx.KubectlVersion == "" {
		ver, err := kubectl.Version(ctx)
		if err != nil {
			ctx.Logger.Fatalf("Failed to get kubectl version info: %v", err)
		}
		ctx.KubectlVersion = ver
		ctx.Logger.Debug("Using kubectl version: ", strings.TrimSpace(ver))
	}

	// Override wild card labels at the chart level. Choose the first chart arbitrarily.
	// Warn on this condition - we should eventually deprecate `get/logs/exec` calls
	// that involve a multi-chart Ankh file.
	wildCardLabels := ctx.AnkhConfig.Kubectl.WildCardLabels
	if charts[0].ChartMeta.WildCardLabels != nil {
		wildCardLabels = *charts[0].ChartMeta.WildCardLabels
		if useWildCardLabels {
			ctx.Logger.Debugf("Using override wildCardLabels %+v from chart %v", wildCardLabels, charts[0].Name)
			if len(ankhFile.Charts) > 1 {
				ctx.Logger.Warnf("Action \"%v\" over multiple charts will be eventually be deprecated",
					ctx.Mode)
			}
		}
	}

	out, err := planAndExecute(ctx, charts, namespace, wildCardLabels)
	if err != nil && ctx.Mode == ankh.Diff {
		ctx.Logger.Warnf("The `diff` feature entered alpha in kubectl v1.9.0, and seems to work best at version v1.12.1. "+
			"Your results may vary. Current kubectl version string is `%s`", ctx.KubectlVersion)
	}
	check(err)

	if out != "" {
		fmt.Println(out)
	}
}

func executeAnkhFile(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile) {
	err := reconcileMissingConfigs(ctx, ankhFile)
	check(err)

	logExecuteAnkhFile(ctx, ankhFile)

	if ctx.HelmVersion == "" {
		ver, err := helm.Version(ctx)
		if err != nil {
			ctx.Logger.Fatalf("Failed to get helm version info: %v", err)
		}
		ctx.HelmVersion = ver
		trimmed := strings.TrimSpace(ver)
		ctx.Logger.Debug("Using Helm version: ", trimmed)

		// Helm's version command is, itself, not written in a backwads compatible
		// way. We choose the 'Client: ' magic sting to prove that Helm is version 2,
		// because Tiller and the "client" distinction was removed in Helm 3+.
		if strings.HasPrefix(trimmed, "Client: ") {
			ctx.HelmV2 = true
			ctx.Logger.Warnf("Helm v2 is no longer maintained as of November 2020, please migrate to Helm v3.\n Info here: https://helm.sh/docs/intro/install/")
		}
	}

	logChartsExecute := func(charts []ankh.Chart, namespace string, extra string) {
		plural := "s"
		n := len(charts)
		if n == 1 {
			plural = ""
		}
		names := []string{}
		for _, chart := range charts {
			names = append(names, chart.Name)
		}
		ctx.Logger.Infof("Using %vnamespace \"%v\" for %v chart%v [ %v ]",
			extra, namespace, n, plural, strings.Join(names, ", "))
	}

	if ctx.Namespace != nil {
		// Namespace overridden on the command line, so use that one for everything.
		namespace := *ctx.Namespace
		logChartsExecute(ankhFile.Charts, namespace, "command-line override ")
		executeChartsOnNamespace(ctx, ankhFile, ankhFile.Charts, namespace)
	} else {
		// Gather charts by namespace, and execute them in sets.
		chartSets := make(map[string][]ankh.Chart)
		for _, chart := range ankhFile.Charts {
			namespace := *chart.ChartMeta.Namespace
			chartSets[namespace] = append(chartSets[namespace], chart)
		}

		// Sort the namespaces. We don't guarantee this behavior, but it's more sane than
		// letting the namespace ordering depend on unorderd golang maps.
		allNamespaces := []string{}
		for namespace, _ := range chartSets {
			allNamespaces = append(allNamespaces, namespace)
		}
		sort.Strings(allNamespaces)
		for _, namespace := range allNamespaces {
			charts := chartSets[namespace]
			logChartsExecute(charts, namespace, "")
			executeChartsOnNamespace(ctx, ankhFile, charts, namespace)
		}
	}
}

func executeContext(ctx *ankh.ExecutionContext, rootAnkhFile *ankh.AnkhFile) {
	dependencies := []string{}
	if ctx.Chart == "" {
		dependencies = rootAnkhFile.Dependencies
	} else {
		log.Debugf("Skipping dependencies since we are operating only on chart %v", ctx.Chart)
	}

	for _, dep := range dependencies {
		log.Infof("Satisfying dependency: %v", dep)

		ankhFilePath := dep
		ankhFile, err := ankh.ParseAnkhFile(ankhFilePath)
		if err == nil {
			ctx.Logger.Debugf("- OK: %v", ankhFilePath)
		}
		check(err)

		ctx.WorkingPath = path.Dir(ankhFilePath)
		executeAnkhFile(ctx, &ankhFile)
		ctx.WorkingPath = ""

		log.Infof("Finished satisfying dependency: %v", dep)
	}

	if len(rootAnkhFile.Charts) > 0 {
		executeAnkhFile(ctx, rootAnkhFile)
	} else if len(dependencies) == 0 {
		if ctx.AnkhConfig.Helm.Repository == "" || ctx.NoPrompt {
			ctx.Logger.Fatalf("No charts nor dependencies provided, nothing to do")
		} else {
			// Prompt for a chart
			ctx.Logger.Infof("No chart specified as an argument, and no `charts` found in an Ankh file")
			charts, err := helm.GetChartNames(ctx, ctx.AnkhConfig.Helm.Repository)
			check(err)

			selectedChart, err := util.PromptForSelection(charts, "Select a chart", false)
			check(err)

			rootAnkhFile.Charts = []ankh.Chart{ankh.Chart{Name: selectedChart}}
			ctx.Logger.Infof("Using chart \"%v\" based on prompt selection", selectedChart)

			executeAnkhFile(ctx, rootAnkhFile)
		}
	}
}

func checkContext(ankhConfig *ankh.AnkhConfig, context string) {
	_, ok := ankhConfig.Contexts[context]
	if !ok {
		log.Errorf("Context '%v' not found in `contexts`", context)
		log.Info("The following contexts are available:")
		printContexts(ankhConfig)
		os.Exit(1)
	}
}

func switchContext(ctx *ankh.ExecutionContext, ankhConfig *ankh.AnkhConfig, context string) {
	if context == "" {
		log.Errorf("No context or environment provided. Provide one using -c/--context or -e/--environment")
		log.Info("The following contexts are available:")
		printContexts(ankhConfig)
		log.Info("The following environments are available:")
		printEnvironments(ankhConfig)
		os.Exit(1)
	}

	checkContext(ankhConfig, context)
	errs := ankhConfig.ValidateAndInit(ctx, context)
	if len(errs) > 0 && !ctx.IgnoreContextAndEnv {
		// The config validation errors are not recoverable.
		log.Fatalf("%v", util.MultiErrorFormat(errs))
	}
}

func planAndExecute(ctx *ankh.ExecutionContext, charts []ankh.Chart, namespace string, wildCardLabels []string) (string, error) {
	switch ctx.Mode {
	case ankh.Template:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
			},
		})
	case ankh.Lint:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
				plan.PlanStage{Stage: helm.NewLintStage()},
			},
		})
	case ankh.Logs:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
				plan.PlanStage{Stage: kubectl.NewPodSelectionStage()},
				plan.PlanStage{Stage: kubectl.NewLogStage()},
			},
		})
	case ankh.Exec:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
				plan.PlanStage{Stage: kubectl.NewPodSelectionStage()},
				plan.PlanStage{Stage: kubectl.NewExecStage()},
			},
		})
	case ankh.Pods:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
				plan.PlanStage{Stage: kubectl.NewPodStage()},
			},
		})
	case ankh.Get:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
				plan.PlanStage{Stage: kubectl.NewGetStage()},
			},
		})
	case ankh.Rollback:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
				plan.PlanStage{Stage: kubectl.NewRollbackStage()},
			},
		})
	case ankh.Diff:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
				plan.PlanStage{Stage: kubectl.NewDiffStage()},
			},
		})
	case ankh.Explain:
		fallthrough
	case ankh.Apply:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
				plan.PlanStage{Stage: kubectl.NewApplyStage()},
			},
		})
	case ankh.Deploy:
		return plan.Execute(ctx, namespace, wildCardLabels, &plan.Plan{
			PlanStages: []plan.PlanStage{
				plan.PlanStage{Stage: helm.NewTemplateStage(charts)},
				plan.PlanStage{Stage: kubectl.NewCheckStage(), Opts: plan.StageOpts{
					PreExecute: func() bool {
						// TODO better messaging
						ctx.Logger.Infof("Checking to see that objects exist before applying...")
						return true
					},
					OnFailure: func() bool {
						// TODO better messaging
						ctx.Logger.Warnf("Some objects do not yet exist. Apply will create the objects listed above.")
						selection, err := util.PromptForSelection([]string{"Abort", "OK"},
							"Are you certain that you want to continue to create new objects? Select OK to proceed.", false)
						check(err)

						if selection != "OK" {
							ctx.Logger.Fatalf("Aborted.")
						}
						return true
					},
					PassThroughInput: true,
				}},
				plan.PlanStage{Stage: kubectl.NewApplyStage(), Opts: plan.StageOpts{
					PreExecute: func() bool {
						ctx.Logger.Infof("Applying...")
						return true
					},
					PassThroughInput: true,
				}},
				plan.PlanStage{Stage: kubectl.NewPodStage(), Opts: plan.StageOpts{
					PreExecute: func() bool {
						// Evil hack
						ctx.Logger.Infof("Watching pods... (press control-C to stop watching and continue)")
						ctx.ExtraArgs = append(ctx.ExtraArgs, "-w")
						ctx.ShouldCatchSignals = true
						return true
					},
					PassThroughInput: true,
				}},
				plan.PlanStage{Stage: kubectl.NewRollbackStage(), Opts: plan.StageOpts{
					PreExecute: func() bool {
						// Evil hack
						ctx.ShouldCatchSignals = false
						ctx.ExtraArgs = []string{}

						selection, err := util.PromptForSelection([]string{"OK", "Rollback"},
							"Finished. Select OK to continue, or Rollback to rollback.", false)
						check(err)

						if selection == "OK" {
							return false
						}

						ctx.Logger.Warnf("Rolling back... (kubectl output below may be terse)")
						return true
					},
				}},
			},
		})
	default:
		panic(fmt.Sprintf("Missing plan handler for mode %v!", ctx.Mode))
	}
}
