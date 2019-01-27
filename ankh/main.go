package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/signal"
	"path"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/imdario/mergo"
	"github.com/jawher/mow.cli"
	"github.com/mattn/go-isatty"
	"github.com/sirupsen/logrus"

	"gopkg.in/yaml.v2"

	"github.com/appnexus/ankh/config"
	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/docker"
	"github.com/appnexus/ankh/helm"
	"github.com/appnexus/ankh/kubectl"
	"github.com/appnexus/ankh/slack"
	"github.com/appnexus/ankh/util"
)

var AnkhBuildVersion string = "DEVELOPMENT"

var log = logrus.New()

func setLogLevel(ctx *ankh.ExecutionContext, level logrus.Level) {
	if ctx.Quiet {
		log.Level = logrus.ErrorLevel
	} else if ctx.Verbose {
		log.Level = logrus.DebugLevel
	} else {
		log.Level = level
	}
}

func signalHandler(ctx *ankh.ExecutionContext, sigs chan os.Signal) {
	process, _ := os.FindProcess(os.Getpid())
	for {
		sig := <-sigs
		if !ctx.CatchSignals {
			// This appears to work, but still doesn't seem totally right.
			signal.Stop(sigs)
			process.Signal(sig)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		}
	}
}

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

func promptForMissingConfigs(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile) error {
	if ctx.NoPrompt {
		for i := 0; i < len(ankhFile.Charts); i++ {
			chart := &ankhFile.Charts[i]

			// Fetch and merge chart metadata
			meta, err := helm.FetchChartMeta(ctx, chart)
			if err != nil {
				return fmt.Errorf("Error fetching chart \"%v\": %v", chart.Name, err)
			}
			mergo.Merge(&chart.ChartMeta, meta)

			// This logic is unfortunately duplicated in this function.
			// The gist is that if ctx.Namespace is set then we have a
			// command line override, which we'll use later. If the namespace
			// is set on the ChartMeta, then we'll prioritize using that.
			if ctx.Namespace == nil && chart.ChartMeta.Namespace == nil {
				return fmt.Errorf("Missing namespace for chart \"%v\". To use this chart "+
					"without a namespace, use `ankh --namespace \"\" ...`",
					chart.Name)
			}
		}
		return nil
	}

	// Make sure that we don't use the tag argument for more than one Chart.
	// When this happens, it is almost always an error, because a tag value
	// is typically only valid/intended for a single chart.
	tagArgumentUsedForChart := ""

	// Prompt for chart versions if any are missing
	for i := 0; i < len(ankhFile.Charts); i++ {
		chart := &ankhFile.Charts[i]

		if chart.Path == "" && chart.Version == "" {
			versions, err := helm.ListVersions(ctx, chart.Name, true)
			if err != nil {
				return err
			}

			ctx.Logger.Infof("Found chart \"%v\" without a version", chart.Name)
			selectedVersion, err := util.PromptForSelection(strings.Split(strings.Trim(versions, "\n "), "\n"),
				fmt.Sprintf("Select a version for chart \"%v\"", chart.Name))
			if err != nil {
				return err
			}

			chart.Version = selectedVersion
			ctx.Logger.Infof("Using chart \"%v\" at version \"%v\" based on prompt selection", chart.Name, chart.Version)
		} else if chart.Path != "" {
			ctx.Logger.Infof("Using chart \"%v\" from local path \"%v\"", chart.Name, chart.Path)
		}

		// Now that we have either a version or a local path, fetch the chart metadata and merge it.
		meta, err := helm.FetchChartMeta(ctx, chart)
		if err != nil {
			return fmt.Errorf("Error fetching chart \"%v\": %v", chart.Name, err)
		}
		mergo.Merge(&chart.ChartMeta, meta)

		// If namespace is set on the command line, we'll use that as an
		// override later during executeChartsOnNamespace, so don't check
		// for anything here.
		if ctx.Namespace == nil {
			if ankhFile.Namespace != nil && chart.ChartMeta.Namespace == nil {
				ctx.Logger.Infof("Using namespace \"%v\" from Ankh file "+
					"for chart \"%v\" which has no explicit namespace set",
					*ankhFile.Namespace, chart.Name)
				chart.ChartMeta.Namespace = ankhFile.Namespace
			} else if chart.ChartMeta.Namespace == nil {
				ctx.Logger.Infof("Found chart \"%v\" without a namespace", chart.Name)
				if len(ctx.AnkhConfig.Namespaces) > 0 {
					selectedNamespace, err := util.PromptForSelection(ctx.AnkhConfig.Namespaces,
						fmt.Sprintf("Select a namespace for chart '%v' (or re-run with -n/--namespace to provide your own)", chart.Name))
					if err != nil {
						return err
					}
					chart.ChartMeta.Namespace = &selectedNamespace
				} else {
					providedNamespace, err := util.PromptForInput("",
						fmt.Sprintf("Provide a namespace for chart '%v' > ", chart.Name))
					if err != nil {
						return err
					}
					chart.ChartMeta.Namespace = &providedNamespace

				}
				ctx.Logger.Infof("Using namespace \"%v\" for chart \"%v\" based on prompt selection", *chart.ChartMeta.Namespace, chart.Name)
			} else {
				ctx.Logger.Infof("Using namespace \"%v\" for chart \"%v\" based on ankh.yaml present in the chart", *chart.ChartMeta.Namespace, chart.Name)
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
			image := ""
			if chart.ChartMeta.TagImage != "" {
				// No need to prompt for an image name if we already have one in the chart metdata
				image = chart.ChartMeta.TagImage
				ctx.Logger.Infof("Using tagImage \"%v\" for chart \"%v\" based on ankh.yaml present in the chart", chart.ChartMeta.TagImage, chart.Name)
			} else {
				ctx.Logger.Infof("Found chart \"%v\" without a value for \"%v\" ", chart.Name, tagKey)
				defaultValue := chart.Name
				image, err = util.PromptForInput(defaultValue,
					fmt.Sprintf("No tag specified for chart '%v'. Provide the name of an image to select a tag for, "+
						"or nothing to skip this step > ", chart.Name))
				check(err)
			}

			if image == "" {
				ctx.Logger.Infof("Skipping tag prompt since no image name was provided")
				continue
			}

			output, err := docker.ListTags(ctx, image, true)
			check(err)

			trimmedOutput := strings.Trim(output, "\n ")
			if trimmedOutput != "" {
				tags := strings.Split(trimmedOutput, "\n")
				tag, err := util.PromptForSelection(tags, fmt.Sprintf("Select a value for \"%v\"", tagKey))
				check(err)

				ctx.Logger.Infof("Using implicit \"--set tag %v=%s\" based on prompt selection", tagKey, tag)
				chart.Tag = &tag
			} else if image != "" {
				complaint := fmt.Sprintf("Could not determine a tag value, and we check for this because `tagKey` is configured to be `%v`. "+
					"You may want to try passing a tag value explicitly using `ankh --set %v=... `, or simply ignore "+
					"this error entirely using `ankh --ignore-config-errors ...` (not recommended)",
					tagKey, tagKey)
				if ctx.IgnoreConfigErrors {
					ctx.Logger.Warnf("%v", complaint)
				} else {
					ctx.Logger.Fatalf("%v", complaint)
				}
			}
		}

		// we should finally have a tag value
		if ctx.SlackChannel != "" {
			ctx.SlackDeploymentVersion = *chart.Tag
		}
	}

	return nil
}

func filterOutput(ctx *ankh.ExecutionContext, helmOutput string) string {
	ctx.Logger.Debugf("Filtering with inclusive list `%v`", ctx.Filters)

	// The golang yaml library doesn't actually support whitespace/comment
	// preserving round-trip parsing. So, we're going to filter the "hard way".
	filtered := []string{}
	objs := strings.Split(helmOutput, "---")
	for _, obj := range objs {
		lines := strings.Split(obj, "\n")
		for _, line := range lines {
			if !strings.HasPrefix(line, "kind:") {
				continue
			}
			matched := false
			for _, s := range ctx.Filters {
				kind := strings.Trim(line[5:], " ")
				if strings.EqualFold(kind, s) {
					matched = true
					break
				}
			}
			if matched {
				filtered = append(filtered, obj)
				break
			}
		}
	}

	return "---" + strings.Join(filtered, "---")
}

func logExecuteAnkhFile(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile) {
	action := ""
	switch ctx.Mode {
	case ankh.Apply:
		action = "Applying chart"
	case ankh.Rollback:
		action = "Rolling back Deployment/StatefulSet from chart"
	case ankh.Diff:
		action = "Diffing objects from chart"
	case ankh.Exec:
		action = "Exec'ing on pods from chart"
	case ankh.Explain:
		action = "Explaining"
	case ankh.Get:
		action = "Getting objects from chart"
	case ankh.Pods:
		action = "Getting pods for Deployment/StatefulSet from chart"
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
		if ctx.Mode == ankh.Rollback {
			ctx.SlackDeploymentVersion = "rollback"
		}
		err := slack.PingSlackChannel(ctx)
		if err != nil {
			ctx.Logger.Errorf("Slack message failed with error: %v", err)
		}
	}

}

func executeChartsOnNamespace(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile,
	charts []ankh.Chart, namespace string) {
	// Template, then filter.
	helmOutput, err := helm.Template(ctx, charts, namespace)
	check(err)
	if len(ctx.Filters) > 0 {
		helmOutput = filterOutput(ctx, helmOutput)
	}

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
		fallthrough
	case ankh.Explain:
		fallthrough
	case ankh.Rollback:
		fallthrough
	case ankh.Apply:
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
		wildCardLabels := []string{}
		if useWildCardLabels {
			wildCardLabels = ctx.AnkhConfig.Kubectl.WildCardLabels
			if charts[0].ChartMeta.WildCardLabels != nil {
				wildCardLabels = *charts[0].ChartMeta.WildCardLabels
				ctx.Logger.Debugf("Using override wildCardLabels %+v from chart %v", wildCardLabels, charts[0].Name)
				if len(ankhFile.Charts) > 1 {
					ctx.Logger.Warnf("Action \"%v\" over multiple charts will be eventually be deprecated",
						ctx.Mode)
				}
			}
		}

		kubectlOutput, err := kubectl.Execute(ctx, helmOutput, namespace, wildCardLabels, nil)
		if err != nil && ctx.Mode == ankh.Diff {
			ctx.Logger.Warnf("The `diff` feature entered alpha in kubectl v1.9.0, and seems to work best at version v1.12.1. "+
				"Your results may vary. Current kubectl version string is `%s`", ctx.KubectlVersion)
		}
		check(err)

		if ctx.Mode == ankh.Explain {
			// Sweet string badnesss.
			helmOutput = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(helmOutput), "&& \\"))
			fmt.Println(fmt.Sprintf("(%s) | \\\n%s", helmOutput, kubectlOutput))
		} else if kubectlOutput != "" {
			fmt.Println(kubectlOutput)
		}
	case ankh.Template:
		fmt.Println(helmOutput)
	case ankh.Lint:
		errors := helm.Lint(ctx, helmOutput, *ankhFile)
		if len(errors) > 0 {
			for _, err := range errors {
				ctx.Logger.Warningf("%v", err)
			}
			log.Fatalf("Lint found %d errors", len(errors))
		}

		ctx.Logger.Infof("No issues")
	}
}

func executeAnkhFile(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile) {
	err := promptForMissingConfigs(ctx, ankhFile)
	check(err)

	logExecuteAnkhFile(ctx, ankhFile)

	if ctx.HelmVersion == "" {
		ver, err := helm.Version(ctx)
		if err != nil {
			ctx.Logger.Fatalf("Failed to get helm version info: %v", err)
		}
		ctx.HelmVersion = ver
		ctx.Logger.Debug("Using helm version: ", strings.TrimSpace(ver))
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
		if ctx.NoPrompt {
			ctx.Logger.Fatalf("No charts nor dependencies provided, nothing to do")
		} else if ctx.AnkhConfig.Helm.Registry != "" {
			// Prompt for a chart
			ctx.Logger.Infof("No chart specified as an argument, and no `charts` found in an Ankh file")
			charts, err := helm.GetChartNames(ctx)
			check(err)

			selectedChart, err := util.PromptForSelection(charts, "Select a chart")
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
	}

	checkContext(ankhConfig, context)
	errs := ankhConfig.ValidateAndInit(ctx, context)
	if len(errs) > 0 && !ctx.IgnoreContextAndEnv {
		// The config validation errors are not recoverable.
		log.Fatalf("%v", util.MultiErrorFormat(errs))
	}
}

func main() {
	app := cli.App("ankh", "Another Kubernetes Helper")
	app.Spec = "[--verbose] [--quiet] [--no-prompt] [--ignore-config-errors] [--ankhconfig] [--kubeconfig] [--datadir] [--release] [--context] [--environment] [--namespace] [--tag] [--set...]"

	var (
		verbose            = app.BoolOpt("v verbose", false, "Verbose debug mode")
		quiet              = app.BoolOpt("q quiet", false, "Quiet mode. Critical logging only. The quiet option overrides the verbose option.")
		noPrompt           = app.BoolOpt("no-prompt", false, "Do not prompt for missing required configuration. Exit with non-zero status and a fatal log message instead.")
		ignoreConfigErrors = app.BoolOpt("ignore-config-errors", false, "Ignore certain configuration errors that have defined, but potentially dangerous behavior.")
		ankhconfig         = app.String(cli.StringOpt{
			Name:   "ankhconfig",
			Value:  path.Join(os.Getenv("HOME"), ".ankh", "config"),
			Desc:   "The ankh config to use. ANKHCONFIG may be set to include a list of ankh configs to merge. Similar behavior to kubectl's KUBECONFIG.",
			EnvVar: "ANKHCONFIG",
		})
		kubeconfig = app.String(cli.StringOpt{
			Name:   "kubeconfig",
			Value:  path.Join(os.Getenv("HOME"), ".kube/config"),
			Desc:   "The kube config to use when invoking kubectl",
			EnvVar: "KUBECONFIG",
		})
		release = app.String(cli.StringOpt{
			Name:   "r release",
			Value:  "",
			Desc:   "The release to use. Must provide this, or have a release already present in the target context",
			EnvVar: "ANKHRELEASE",
		})
		context = app.String(cli.StringOpt{
			Name:   "c context",
			Value:  "",
			Desc:   "The context to use. Must provide this, or an environment via --environment",
			EnvVar: "ANKHCONTEXT",
		})
		environment = app.String(cli.StringOpt{
			Name:   "e environment",
			Value:  "",
			Desc:   "The environment to use. Must provide this, or an individual context via `--context`",
			EnvVar: "ANKHENVIRONMENT",
		})
		namespaceSet = false
		namespace    = app.String(cli.StringOpt{
			Name:      "n namespace",
			Value:     "",
			Desc:      "The namespace to use with kubectl. Optional. Overrides any namespace provided in an Ankh file.",
			SetByUser: &namespaceSet,
		})
		tagSet = false
		tag    = app.String(cli.StringOpt{
			Name:      "t tag",
			Value:     "",
			Desc:      "The tag value to use. This value is passed to helm as `--set $tagKey=$tag`. Requires a `tagKey` to be configured, either on the `chart` in an Ankh file, or in an `ankh.yaml` inside the Helm chart. Only valid when Ankh has a single chart to operate over, eg: with `--chart` or when an Ankh file has one chart entry.",
			SetByUser: &tagSet,
		})
		datadir = app.String(cli.StringOpt{
			Name:   "datadir",
			Value:  path.Join("/tmp", ".ankh", "data"),
			Desc:   "The data directory for Ankh template history",
			EnvVar: "ANKHDATADIR",
		})
		helmSet = app.Strings(cli.StringsOpt{
			Name:  "set",
			Desc:  "Variables passed through to helm via --set",
			Value: []string{},
		})
	)

	log.Out = os.Stdout
	log.Formatter = &util.CustomFormatter{
		IsTerminal: isatty.IsTerminal(os.Stdout.Fd()),
	}

	ctx := &ankh.ExecutionContext{}

	app.Before = func() {
		setLogLevel(ctx, logrus.InfoLevel)

		helmVars := map[string]string{}
		for _, helmkvPair := range *helmSet {
			k := strings.SplitN(helmkvPair, "=", 2)
			if len(k) != 2 {
				log.Fatalf("Malformed --set argument '%v' (could not split on '='). Set arguments must be passed as 'key=value'", helmkvPair)
			} else {
				helmVars[k[0]] = k[1]
			}
		}

		if *context != "" && *environment != "" {
			log.Fatalf("Must not provide both `--context` and `--environment`, because an environment maps to one or more contexts.")
		}

		var namespaceOpt *string
		if namespaceSet {
			namespaceOpt = namespace
		}

		var tagOpt *string
		if tagSet {
			tagOpt = tag
		}

		ctx = &ankh.ExecutionContext{
			Verbose:             *verbose,
			Quiet:               *quiet,
			AnkhConfigPath:      *ankhconfig,
			KubeConfigPath:      *kubeconfig,
			Context:             *context,
			Release:             *release,
			Environment:         *environment,
			Namespace:           namespaceOpt,
			Tag:                 tagOpt,
			DataDir:             path.Join(*datadir, fmt.Sprintf("%v-%v", time.Now().Unix(), rand.Intn(100000))),
			Logger:              log,
			HelmSetValues:       helmVars,
			IgnoreContextAndEnv: ctx.IgnoreContextAndEnv,
			IgnoreConfigErrors:  ctx.IgnoreConfigErrors || *ignoreConfigErrors,
			NoPrompt:            *noPrompt,
		}

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		go signalHandler(ctx, sigs)

		if ctx.Verbose && ctx.Quiet {
			// Quiet overrides verbose, since it's more likely that the user
			// requires certain invocations to be quiet, and may be composing
			// with other tools that set verbose by default. Somewhat contrived
			// situation, but it feels right.
			ctx.Verbose = false
		}

		// Default to info level logging
		setLogLevel(ctx, logrus.InfoLevel)

		log.Debugf("Using KubeConfigPath %v (KUBECONFIG = '%v')", ctx.KubeConfigPath, os.Getenv("KUBECONFIG"))
		log.Debugf("Using AnkhConfigPath %v (ANKHCONFIG = '%v')", ctx.AnkhConfigPath, os.Getenv("ANKHCONFIG"))

		mergedAnkhConfig := ankh.AnkhConfig{}
		parsedConfigs := make(map[string]bool)
		configPaths := strings.Split(ctx.AnkhConfigPath, ",")
		for len(configPaths) > 0 {
			configPath := configPaths[0]
			configPaths = configPaths[1:]

			if parsedConfigs[configPath] {
				log.Debugf("Already parsed %v", configPath)
				continue
			}

			log.Debugf("Using config from path %v", configPath)

			ankhConfig, err := config.GetAnkhConfig(ctx, configPath)
			if err != nil {
				// TODO: this is a mess
				if !ctx.IgnoreContextAndEnv && !ctx.IgnoreConfigErrors {
					// The config validation errors are not recoverable.
					log.Fatalf("%s: Rerun with `ankh --ignore-config-errors ...` to ignore this error and use the merged configuration anyway.", err)
				} else {
					log.Warnf("%v", err)
				}
			}

			// Warn on context and environment conflict, since this case is almost certainly unintentional.
			for name, _ := range ankhConfig.Contexts {
				if context, ok := mergedAnkhConfig.Contexts[name]; ok {
					complaint := fmt.Sprintf("Context `%v` already defined from config source `%v`, would have been overriden by config source `%v`.",
						name, context.Source, configPath)
					if !ctx.IgnoreConfigErrors {
						log.Fatalf(complaint + " Rerun with `ankh --ignore-config-errors ...` to ignore this error and use the merged configuration anyway.")
					} else {
						log.Warnf(complaint)
					}
				}
			}
			for name, _ := range ankhConfig.Environments {
				if environment, ok := mergedAnkhConfig.Environments[name]; ok {
					complaint := fmt.Sprintf("Environment `%v` already defined from config source `%v`, would have been overriden by config source `%v`.",
						name, environment.Source, configPath)
					if !ctx.IgnoreConfigErrors {
						log.Fatalf(complaint + " Rerun with `ankh --ignore-config-errors ...` to ignore this error and use the merged configuration anyway.")
					} else {
						log.Warnf(complaint)
					}
				}
			}

			// Merge it in. We'll need to dedup arrays later.
			mergo.Merge(&mergedAnkhConfig, ankhConfig)

			// Follow includes, mark this one as visited.
			configPaths = append(configPaths, ankhConfig.Include...)
			parsedConfigs[configPath] = true
		}

		// Don't accidentally wind up in an include cycle.
		mergedAnkhConfig.Include = util.ArrayDedup(mergedAnkhConfig.Include)

		if ctx.Context != "" {
			mergedAnkhConfig.CurrentContextName = ctx.Context
		}
		if ctx.Environment == "" && !ctx.IgnoreContextAndEnv {
			log.Debugf("Switching to context %v", mergedAnkhConfig.CurrentContextName)
			switchContext(ctx, &mergedAnkhConfig, mergedAnkhConfig.CurrentContextName)
		}

		// Save the original config, and then assume the mergedAnkhConfig as the config going forward.
		ctx.OriginalAnkhConfig = ctx.AnkhConfig
		ctx.AnkhConfig = mergedAnkhConfig
	}

	app.Command("explain", "Explain how an Ankh file would be applied to a Kubernetes cluster", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart] [--chart-path]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the explain command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Explain

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("apply", "Apply an Ankh file to a Kubernetes cluster", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--dry-run] [--chart] [--chart-path] [--slack] [--slack-message] [--filter...]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		dryRun := cmd.BoolOpt("dry-run", false, "Perform a dry-run and don't actually apply anything to a cluster")
		chart := cmd.StringOpt("chart", "", "Limits the apply command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		slackChannel := cmd.StringOpt("s slack", "", "Send slack message to specified slack channel about application update")
		slackMessageOverride := cmd.StringOpt("m slack-message", "", "Override the default slack message being sent")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = *dryRun
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Apply
			ctx.SlackChannel = *slackChannel
			ctx.SlackMessageOverride = *slackMessageOverride
			filters := []string{}
			for _, filter := range *filter {
				filters = append(filters, string(filter))
			}
			ctx.Filters = filters

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("rollback", "Rollback deployments associated with a templated Ankh file from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--dry-run] [--chart] [--chart-path] [--slack] [--slack-message]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		dryRun := cmd.BoolOpt("dry-run", false, "Perform a dry-run and don't actually rollback anything to a cluster")
		chart := cmd.StringOpt("chart", "", "Limits the rollback command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		slackChannel := cmd.StringOpt("s slack", "", "Send slack message to specified slack channel about application update")
		slackMessageOverride := cmd.StringOpt("m slack-message", "", "Override the default slack message being sent")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = *dryRun
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Rollback
			ctx.SlackChannel = *slackChannel
			ctx.SlackMessageOverride = *slackMessageOverride
			ctx.Filters = []string{"deployment", "statfulset"}

			ctx.Logger.Warnf("Rollback is not a transactional operation.\n" +
				"\n" +
				"Rollback uses `kubectl rollout undo` which only rolls back ReplicaSet specs under Deployment and StatefulSet objects.\n" +
				"\n" +
				"This design has two notable limitations in the context of Ankh, Helm, and templated object manifests:\n" +
				"1) Manifest attributes such as labels are NOT rolled back. This can be problematic for use cases that visually track " +
				"object history using labels or annotations. It is almost certain that the resulting Deployment and ReplicaSet will appear inconsistent.\n" +
				"2) Other Chart objects, such as ConfigMaps and Services, are by design not rolled back. This can be problematic for use cases that attempt " +
				"to apply charts atomically, where the Deployment spec has a hard dependency on an associated Service or ConfigMap. Rollout undo will NOT " +
				"do the right thing in this case. You MUST `ankh ... apply` using the co-dependent chart and tag value in order to converge back to a correct state.\n" +
				"\n" +
				"If you already know the chart version and associated tag values (eg: `--set ...`) that you want to converge to, use `ankh --set $... apply --chart $chartName@$prevVersion` instead.\n")
			selection, err := util.PromptForSelection([]string{"Abort", "OK"},
				"Are you certain that you want to run `kubectl rollout undo` to rollback to a previous ReplicaSet spec? Select OK to proceed.")
			check(err)

			if selection != "OK" {
				ctx.Logger.Fatalf("Aborting")
			}

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("diff", "Diff against live objects associated with a templated Ankh file from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart] [--chart-path] [--filter...]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the apply command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action")

		cmd.Action = func() {
			setLogLevel(ctx, logrus.InfoLevel)
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = false
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Diff
			filters := []string{}
			for _, filter := range *filter {
				filters = append(filters, string(filter))
			}
			ctx.Filters = filters

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("get", "Get objects associated with a templated Ankh file from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart] [--chart-path] [--filter...] [EXTRA...]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the apply command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action")
		extra := cmd.StringsArg("EXTRA", []string{}, "Extra arguments to pass to `kubectl`, which can be specified after `--` eg: `ankh ... get -- -o json`")

		cmd.Action = func() {
			setLogLevel(ctx, logrus.InfoLevel)
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = false
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Get
			filters := []string{}
			for _, filter := range *filter {
				filters = append(filters, string(filter))
			}
			ctx.Filters = filters
			for _, e := range *extra {
				ctx.Logger.Debugf("Appending extra arg: %+v", e)
				ctx.ExtraArgs = append(ctx.ExtraArgs, e)
			}

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("pods", "Get pods associated with a templated Ankh file from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [-w] [-d] [--chart] [--chart-path] [EXTRA...]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the apply command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		watch := cmd.BoolOpt("w watch", false, "Watch for updates (ie: pass -w to kubectl)")
		describe := cmd.BoolOpt("d describe", false, "Use `kubectl describe ...` instead of `kubectl get -o wide ...` for pods")
		extra := cmd.StringsArg("EXTRA", []string{}, "Extra arguments to pass to `kubectl`, which can be specified after `--` eg: `ankh ... get -- -o json`")

		cmd.Action = func() {
			setLogLevel(ctx, logrus.InfoLevel)
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = false
			ctx.Describe = *describe
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Pods
			for _, e := range *extra {
				ctx.Logger.Debugf("Appending extra arg: %+v", e)
				ctx.ExtraArgs = append(ctx.ExtraArgs, e)
			}
			if *watch {
				ctx.Logger.Debug("Appending watch args as extra args")
				ctx.ExtraArgs = append(ctx.ExtraArgs, "-w")
				ctx.ShouldCatchSignals = true
			}

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("logs", "Get logs for pods associated with a templated Ankh file from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-c] [-f] [--filename] [--previous] [--tail] [--chart] [--chart-path] [CONTAINER]"

		ankhFilePath := cmd.StringOpt("filename", "ankh.yaml", "Config file name")
		numTailLines := cmd.IntOpt("t tail", 10, "The number of most recent log lines to see. Pass 0 to receive all log lines available from Kubernetes, which is subject to its own retential policy.")
		follow := cmd.BoolOpt("f", false, "Follow logs")
		previous := cmd.BoolOpt("p previous", false, "Get logs for the previously terminated container, if any")
		chart := cmd.StringOpt("chart", "", "Limits the apply command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		container := cmd.StringOpt("c container", "", "The container to exec on. Required when there is more than one container running in the pods associated with the templated Ankh file.")
		containerArg := cmd.StringArg("CONTAINER", "", "The container to get logs for. Required when there is more than one container running in the pods associated with the templated Ankh file.")

		cmd.Action = func() {
			setLogLevel(ctx, logrus.InfoLevel)
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = false
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Logs
			if *follow {
				ctx.ExtraArgs = append(ctx.ExtraArgs, "-f")
				ctx.ShouldCatchSignals = true
			}
			if *previous {
				ctx.ExtraArgs = append(ctx.ExtraArgs, "--previous")
			}
			if *container != "" && *containerArg != "" && *container != *containerArg {
				ctx.Logger.Fatalf("Conflicting positional argument '%v' and container option (-c) '%v'. Please ensure that these are the same, or only use one one.",
					*containerArg, *container)
			}
			if *container != "" {
				ctx.ExtraArgs = append(ctx.ExtraArgs, []string{"-c", *container}...)
			} else if *containerArg != "" {
				ctx.ExtraArgs = append(ctx.ExtraArgs, []string{"-c", *containerArg}...)
			}
			if *numTailLines > 0 {
				n := strconv.FormatInt(int64(*numTailLines), 10)
				ctx.ExtraArgs = append(ctx.ExtraArgs, []string{"--tail", n}...)
			}
			ctx.Logger.Debugf("Using extraArgs %+v", ctx.ExtraArgs)

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("exec", "Exec a command on pods associated with a templated Ankh file from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-c] [--filename] [--chart] [--chart-path] [PASSTHROUGH...]"

		ankhFilePath := cmd.StringOpt("filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the apply command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		container := cmd.StringOpt("c container", "", "The container to exec on. Required when there is more than one container running in the pods associated with the templated Ankh file.")
		extra := cmd.StringsArg("PASSTHROUGH", []string{}, "Pass-through arguments to provide to `kubectl` after `exec`, which can be specified after `--` eg: `ankh ... get -- -o json`")

		cmd.Action = func() {
			setLogLevel(ctx, logrus.InfoLevel)
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = false
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Exec
			if *container != "" {
				ctx.ExtraArgs = append(ctx.ExtraArgs, []string{"-c", *container}...)
			}
			if len(*extra) == 0 {
				*extra = []string{"/bin/sh"}
			}
			for _, e := range *extra {
				ctx.Logger.Debugf("Appending extra arg: %+v", e)
				ctx.PassThroughArgs = append(ctx.PassThroughArgs, e)
			}

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("lint", "Lint an Ankh file, checking for possible errors or mistakes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart] [--chart-path] [--filter...]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the lint command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Lint
			filters := []string{}
			for _, filter := range *filter {
				filters = append(filters, string(filter))
			}
			ctx.Filters = filters

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("template", "Output the results of templating an Ankh file", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart] [--chart-path] [--filter...]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the template command to only the specified chart")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Template
			filters := []string{}
			for _, filter := range *filter {
				filters = append(filters, string(filter))
			}
			ctx.Filters = filters

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("image", "Manage Docker images", func(cmd *cli.Cmd) {
		ctx.IgnoreContextAndEnv = true
		ctx.IgnoreConfigErrors = true

		cmd.Command("tags", "List tags for a Docker image", func(cmd *cli.Cmd) {
			cmd.Spec = "IMAGE"
			image := cmd.StringArg("IMAGE", "", "The docker image to fetch tags for")

			cmd.Action = func() {
				output, err := docker.ListTags(ctx, *image, false)
				check(err)
				if output != "" {
					fmt.Println(output)
				}
				os.Exit(0)
			}
		})

		cmd.Command("ls", "List images for a Docker repository", func(cmd *cli.Cmd) {
			cmd.Spec = "[-n]"
			numToShow := cmd.IntOpt("n num", 5, "Number of tags to show, fuzzy-sorted descending by semantic version. Pass zero to see all versions.")

			cmd.Action = func() {
				output, err := docker.ListImages(ctx, *numToShow)
				check(err)
				if output != "" {
					fmt.Printf(output)
				}
				os.Exit(0)
			}
		})
	})

	app.Command("chart", "Manage Helm charts", func(cmd *cli.Cmd) {
		ctx.IgnoreContextAndEnv = true
		ctx.IgnoreConfigErrors = true

		cmd.Command("ls", "List Helm charts and their versions", func(cmd *cli.Cmd) {
			cmd.Spec = "[-n]"
			numToShow := cmd.IntOpt("n num", 5, "Number of versions to show, sorted descending by creation date. Pass zero to see all versions.")

			cmd.Action = func() {
				if ctx.AnkhConfig.Helm.Registry == "" {
					// TODO: Registry should be a global config, not a per-context config
					for name, x := range ctx.AnkhConfig.Contexts {
						ctx.Logger.Infof("Using HelmRegistryURL '%v' taken from the first "+
							"Ankh context '%v'", ctx.AnkhConfig.Helm.Registry, name)
						ctx.AnkhConfig.Helm.Registry = x.HelmRegistryURL
						break
					}
				}

				helmOutput, err := helm.ListCharts(ctx, *numToShow)
				check(err)
				if helmOutput != "" {
					fmt.Printf(helmOutput)
				}
				os.Exit(0)
			}
		})

		cmd.Command("versions", "List versions for a Helm chart", func(cmd *cli.Cmd) {
			cmd.Spec = "CHART"
			chart := cmd.StringArg("CHART", "", "The Helm chart to fetch versions for")

			cmd.Action = func() {
				if ctx.AnkhConfig.Helm.Registry == "" {
					// TODO: Registry should be a global config, not a per-context config
					for name, x := range ctx.AnkhConfig.Contexts {
						ctx.Logger.Infof("Using HelmRegistryURL '%v' taken from the first "+
							"Ankh context '%v'", ctx.AnkhConfig.Helm.Registry, name)
						ctx.AnkhConfig.Helm.Registry = x.HelmRegistryURL
						break
					}
				}

				helmOutput, err := helm.ListVersions(ctx, *chart, false)
				check(err)
				if helmOutput != "" {
					fmt.Println(helmOutput)
				}
				os.Exit(0)
			}
		})

		cmd.Command("inspect", "Inspect a Helm chart", func(cmd *cli.Cmd) {
			cmd.Spec = "CHART"
			chart := cmd.StringArg("CHART", "", "The Helm chart to inspect, passed in the `CHART[@VERSION]` format.")

			cmd.Action = func() {
				if ctx.AnkhConfig.Helm.Registry == "" {
					// TODO: Registry should be a global config, not a per-context config
					for name, x := range ctx.AnkhConfig.Contexts {
						ctx.Logger.Infof("Using HelmRegistryURL '%v' taken from the first "+
							"Ankh context '%v'", ctx.AnkhConfig.Helm.Registry, name)
						ctx.AnkhConfig.Helm.Registry = x.HelmRegistryURL
						break
					}
				}

				helmOutput, err := helm.Inspect(ctx, *chart)
				check(err)
				if helmOutput != "" {
					fmt.Println(helmOutput)
				}
				os.Exit(0)
			}
		})

		cmd.Command("publish", "Publish a Helm chart using files from the current directory", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				if ctx.AnkhConfig.Helm.Registry == "" {
					// TODO: Registry should be a global config, not a per-context config
					for name, x := range ctx.AnkhConfig.Contexts {
						ctx.Logger.Infof("Using HelmRegistryURL '%v' taken from the first "+
							"Ankh context '%v'", ctx.AnkhConfig.Helm.Registry, name)
						ctx.AnkhConfig.Helm.Registry = x.HelmRegistryURL
						break
					}
				}

				err := helm.Publish(ctx)
				check(err)
				os.Exit(0)
			}
		})

		cmd.Command("bump", "Bump a Helm chart's semantic version using Chart.yaml from the current directory", func(cmd *cli.Cmd) {
			cmd.Spec = "[SEMVERTYPE]"
			semVerType := cmd.StringArg("SEMVERTYPE", "patch", "Which part of the semantic version (eg: x.y.z) to bump: \"major\", \"minor\", or \"patch\".")

			cmd.Action = func() {
				err := helm.Bump(ctx, *semVerType)
				check(err)
				os.Exit(0)
			}
		})
	})

	app.Command("config", "Manage Ankh configuration", func(cmd *cli.Cmd) {
		ctx.IgnoreContextAndEnv = true
		ctx.IgnoreConfigErrors = true

		cmd.Command("init", "Initialize Ankh configuration", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				// Use the original, unmerged config. We want to explicitly avoid
				// serializing the contents of any remote configs.
				newAnkhConfig := ctx.OriginalAnkhConfig

				if len(newAnkhConfig.Contexts) == 0 {
					newAnkhConfig.Contexts = map[string]ankh.Context{
						"minikube": {
							KubeContext:      "minikube",
							EnvironmentClass: "dev",
							ResourceProfile:  "constrained",
							Release:          "minikube",
							HelmRegistryURL:  "https://kubernetes-charts.storage.googleapis.com",
						},
					}
					ctx.Logger.Infof("Initializing `contexts` to a single sample context for kube-context `minikube`")
				}

				out, err := yaml.Marshal(newAnkhConfig)
				check(err)

				err = ioutil.WriteFile(ctx.AnkhConfigPath, out, 0644)
				check(err)

				os.Exit(0)
			}
		})

		cmd.Command("view", "View merged Ankh configuration", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				out, err := yaml.Marshal(ctx.AnkhConfig)
				check(err)

				fmt.Print(string(out))
				os.Exit(0)
			}
		})

		cmd.Command("get-contexts", "Get available contexts", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				w := tabwriter.NewWriter(os.Stdout, 0, 8, 8, ' ', 0)
				fmt.Fprintf(w, "NAME\tRELEASE\tENVIRONMENT-CLASS\tRESOURCE-PROFILE\tKUBE-CONTEXT/SERVER\tSOURCE\n")
				keys := []string{}
				for k, _ := range ctx.AnkhConfig.Contexts {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, name := range keys {
					ctx, _ := ctx.AnkhConfig.Contexts[name]
					target := ctx.KubeContext
					if target == "" {
						target = ctx.KubeServer
					}
					fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v\n", name, ctx.Release, ctx.EnvironmentClass, ctx.ResourceProfile, target, ctx.Source)
				}
				w.Flush()
				os.Exit(0)
			}
		})

		cmd.Command("get-environments", "Get available environments", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				w := tabwriter.NewWriter(os.Stdout, 0, 8, 8, ' ', 0)
				fmt.Fprintf(w, "NAME\tCONTEXTS\n")
				keys := []string{}
				for k, _ := range ctx.AnkhConfig.Environments {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, name := range keys {
					env, _ := ctx.AnkhConfig.Environments[name]
					fmt.Fprintf(w, "%v\t%v\t%v\n", name, strings.Join(env.Contexts, ","), env.Source)
				}
				w.Flush()
				os.Exit(0)
			}
		})
	})

	app.Command("version", "Show version info", func(cmd *cli.Cmd) {
		ctx.IgnoreContextAndEnv = true
		ctx.IgnoreConfigErrors = true

		cmd.Action = func() {
			ctx.Logger.Infof("Ankh version info:")
			fmt.Println(AnkhBuildVersion)

			ctx.Logger.Infof("`%v version --client` output:", ctx.AnkhConfig.Helm.Command)
			ver, err := helm.Version(ctx)
			check(err)
			fmt.Print(ver)

			ctx.Logger.Infof("`%v version --client` output:", ctx.AnkhConfig.Kubectl.Command)
			ver, err = kubectl.Version(ctx)
			check(err)
			fmt.Print(ver)

			os.Exit(0)
		}
	})

	app.Run(os.Args)
}

func check(err error) {
	if err != nil {
		log.Fatalf("%v", err)
	}
}
