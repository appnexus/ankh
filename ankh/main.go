package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
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
	"github.com/appnexus/ankh/util"
)

var AnkhBuildVersion string = "DEVELOPMENT"

var log = logrus.New()

func check(err error) {
	if err != nil {
		log.Fatalf("%v", err)
	}
}

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

func main() {
	app := cli.App("ankh", "Another Kubernetes Helper")
	app.Spec = "[--verbose] [--quiet] [--no-prompt] [--ignore-config-errors] [--ankhconfig] [--kubeconfig] [--datadir] [--helmdir] [--release] [--context] [--environment] [--namespace] [--tag] [--set...]"

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
			Desc:      "The namespace to use with kubectl. Optional. Overrides any other ways to set a namespace.",
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
		helmdir = app.String(cli.StringOpt{
			Name:   "helmdir",
			Value:  path.Join("/tmp", ".helm"),
			Desc:   "The local home directory for helm",
			EnvVar: "HELM_HOME",
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
			HelmDir:             *helmdir,
			IgnoreContextAndEnv: ctx.IgnoreContextAndEnv,
			IgnoreConfigErrors:  ctx.IgnoreConfigErrors || *ignoreConfigErrors,
			SkipConfig:          ctx.SkipConfig,
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

		if ctx.SkipConfig {
			log.Debugf("ctx.SkipConfig set, not parsing config before running commands")
			return
		}

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

			ankhConfig, err := config.GetAnkhConfigWithDefaults(ctx, configPath)
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
			if ctx.Context == "" && !ctx.NoPrompt {
				// No environment/context and we can prompt, so do that now.
				if len(mergedAnkhConfig.Environments) > 0 {
					selection, err := util.PromptForSelection(getEnvironmentTable(&mergedAnkhConfig),
						"Select an environment", true)
					check(err)
					fields := strings.Fields(selection)
					ctx.Environment = fields[0]
				} else if len(mergedAnkhConfig.Contexts) > 0 {
					// No context and we can prompt, so do that now.
					selection, err := util.PromptForSelection(getContextTable(&mergedAnkhConfig),
						"Select a context", true)
					check(err)
					fields := strings.Fields(selection)
					ctx.Context = fields[0]
					mergedAnkhConfig.CurrentContextName = ctx.Context
					log.Debugf("Switching to context %v", mergedAnkhConfig.CurrentContextName)
					switchContext(ctx, &mergedAnkhConfig, mergedAnkhConfig.CurrentContextName)
				}
			} else {
				// We either have a context or we're not willing to prompt fo rone.
				// Let it happen.
				log.Debugf("Switching to context %v", mergedAnkhConfig.CurrentContextName)
				switchContext(ctx, &mergedAnkhConfig, mergedAnkhConfig.CurrentContextName)
			}
		}

		// Use the merged config going forward
		ctx.AnkhConfig = mergedAnkhConfig
	}

	app.Command("apply", "Apply one or more charts to Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[--ankhfile] [--dry-run] [--chart] [--chart-path] [--slack] [--slack-message] [--jira-ticket] [--filter...] [--image-tag-filter] [--chart-version-filter]"

		ankhFilePath := cmd.StringOpt("ankhfile", "", "Path to an Ankh file for managing multiple charts")
		dryRun := cmd.BoolOpt("dry-run", false, "Perform a dry-run and don't actually apply anything")
		chart := cmd.StringOpt("chart", "", "The chart to use")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		slackChannel := cmd.StringOpt("s slack", "", "Send slack message to specified slack channel about application update")
		slackMessageOverride := cmd.StringOpt("m slack-message", "", "Override the default slack message being sent")
		createJiraTicket := cmd.BoolOpt("j jira-ticket", false, "Create a JIRA ticket to track update")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action")
		imageTagFilter := cmd.StringOpt("image-tag-filter", "", "Filters out any image tags that include the specified substring. Matching tags will not appear in the prompt.")
		chartVersionFilter := cmd.StringOpt("chart-version-filter", "", "Filters out any chart versions that include the specified substring. Matching versions will not appear in the prompt.")

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
			ctx.CreateJiraTicket = *createJiraTicket
			filters := []string{}
			for _, filter := range *filter {
				filters = append(filters, string(filter))
			}
			ctx.Filters = filters
			ctx.ImageTagFilter = *imageTagFilter
			ctx.ChartVersionFilter = *chartVersionFilter

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("explain", "Explain how one or more charts would be applied to Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[--ankhfile] [--chart] [--chart-path]"

		ankhFilePath := cmd.StringOpt("ankhfile", "", "Path to an Ankh file for managing multiple charts")
		chart := cmd.StringOpt("chart", "", "The chart to use")
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

	app.Command("deploy", "(experimental) Run a multi-stage deployment of a chart to Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[--chart] [--chart-path] [--slack] [--slack-message] [--jira-ticket] [--filter...]"

		chart := cmd.StringOpt("chart", "", "The chart to use")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		slackChannel := cmd.StringOpt("s slack", "", "Send slack message to specified slack channel about application update")
		slackMessageOverride := cmd.StringOpt("m slack-message", "", "Override the default slack message being sent")
		createJiraTicket := cmd.BoolOpt("j jira-ticket", false, "Create a JIRA ticket to track update")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action")

		cmd.Action = func() {
			ctx.Chart = *chart
			if *chartPath != "" {
				ctx.Chart = *chartPath
				ctx.LocalChart = true
			}
			ctx.Mode = ankh.Deploy
			ctx.SlackChannel = *slackChannel
			ctx.SlackMessageOverride = *slackMessageOverride
			ctx.CreateJiraTicket = *createJiraTicket
			filters := []string{}
			for _, filter := range *filter {
				filters = append(filters, string(filter))
			}
			ctx.Filters = filters

			ctx.Logger.Warnf("\"deploy\" is an experimental command.")
			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("rollback", "Rollback deployments associated with one or more charts from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[--ankhfile] [--dry-run] [--chart] [--chart-path] [--slack] [--slack-message] [--jira-ticket] "

		ankhFilePath := cmd.StringOpt("ankhfile", "", "Path to an Ankh file for managing multiple charts")
		dryRun := cmd.BoolOpt("dry-run", false, "Perform a dry-run and don't actually rollback anything")
		chart := cmd.StringOpt("chart", "", "The chart to use")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		slackChannel := cmd.StringOpt("s slack", "", "Send slack message to specified slack channel about application update")
		slackMessageOverride := cmd.StringOpt("m slack-message", "", "Override the default slack message being sent")
		createJiraTicket := cmd.BoolOpt("j jira-ticket", false, "Create a JIRA ticket to track update")

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
			ctx.CreateJiraTicket = *createJiraTicket

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
				"Are you certain that you want to run `kubectl rollout undo` to rollback to a previous ReplicaSet spec? Select OK to proceed.", false)
			check(err)

			if selection != "OK" {
				ctx.Logger.Fatalf("Aborting")
			}

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("diff", "Diff against live objects associated with one or more charts from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[--ankhfile] [--chart] [--chart-path] [--filter...]"

		ankhFilePath := cmd.StringOpt("ankhfile", "", "Path to an Ankh file for managing multiple charts")
		chart := cmd.StringOpt("chart", "", "The chart to use")
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

	app.Command("get", "Get objects associated with a chart from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[--chart] [--chart-path] [--filter...] [EXTRA...]"

		chart := cmd.StringOpt("chart", "", "The chart to use")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action")
		extra := cmd.StringsArg("EXTRA", []string{}, "Extra arguments to pass to `kubectl`, which can be specified after `--` eg: `ankh ... get -- -o json`")

		cmd.Action = func() {
			setLogLevel(ctx, logrus.InfoLevel)
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

	app.Command("pods", "Get pods associated with a chart from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-w] [-d] [--chart] [--chart-path] [EXTRA...]"

		chart := cmd.StringOpt("chart", "", "The chart to use")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		watch := cmd.BoolOpt("w watch", false, "Watch for updates (ie: pass -w to kubectl)")
		describe := cmd.BoolOpt("d describe", false, "Use `kubectl describe ...` instead of `kubectl get -o wide ...` for pods")
		extra := cmd.StringsArg("EXTRA", []string{}, "Extra arguments to pass to `kubectl`, which can be specified after `--` eg: `ankh ... get -- -o json`")

		cmd.Action = func() {
			setLogLevel(ctx, logrus.InfoLevel)
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

	app.Command("logs", "Get logs for a pod associated with a chart from Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-c] [-f] [--previous] [--tail] [--chart] [--chart-path] [CONTAINER]"

		numTailLines := cmd.IntOpt("t tail", 10, "The number of most recent log lines to see. Pass 0 to receive all log lines available from Kubernetes, which is subject to its own retential policy.")
		follow := cmd.BoolOpt("f", false, "Follow logs")
		previous := cmd.BoolOpt("p previous", false, "Get logs for the previously terminated container, if any")
		chart := cmd.StringOpt("chart", "", "The chart to use")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		container := cmd.StringOpt("c container", "", "The container to exec on.")
		containerArg := cmd.StringArg("CONTAINER", "", "The container to get logs for.")

		cmd.Action = func() {
			setLogLevel(ctx, logrus.InfoLevel)
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

	app.Command("exec", "Exec a command on a pod associated with a chart in Kubernetes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-c] [--chart] [--chart-path] [PASSTHROUGH...]"

		chart := cmd.StringOpt("chart", "", "The chart to use")
		chartPath := cmd.StringOpt("chart-path", "", "Use a local chart directory instead of a remote, versioned chart")
		container := cmd.StringOpt("c container", "", "The container to exec the command on")
		extra := cmd.StringsArg("PASSTHROUGH", []string{}, "Pass-through arguments to provide to `kubectl` after `exec`, which can be specified after `--` eg: `ankh ... get -- -o json`")

		cmd.Action = func() {
			setLogLevel(ctx, logrus.InfoLevel)
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

	app.Command("lint", "Lint one or more charts, checking for possible errors or mistakes", func(cmd *cli.Cmd) {
		cmd.Spec = "[--ankhfile] [--chart] [--chart-path] [--filter...]"

		ankhFilePath := cmd.StringOpt("ankhfile", "", "Path to an Ankh file for managing multiple charts")
		chart := cmd.StringOpt("chart", "", "The chart to use")
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

	app.Command("template", "Output the results of templating one or more charts.", func(cmd *cli.Cmd) {
		cmd.Spec = "[--ankhfile] [--chart] [--chart-path] [--filter...]"

		ankhFilePath := cmd.StringOpt("ankhfile", "", "Path to an Ankh file for managing multiple charts")
		chart := cmd.StringOpt("chart", "", "The chart to use")
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
			imageArg := cmd.StringArg("IMAGE", "", "The docker image to fetch tags for")

			cmd.Action = func() {
				registryDomain, image, err := docker.ParseImage(ctx, *imageArg)
				check(err)

				output, err := docker.ListTags(ctx, registryDomain, image, false)
				check(err)
				if output != "" {
					fmt.Println(output)
				}
				os.Exit(0)
			}
		})

		cmd.Command("ls", "List images for a Docker repository", func(cmd *cli.Cmd) {
			cmd.Spec = "[-n] [-r]"
			numToShow := cmd.IntOpt("n num", 5, "Number of tags to show, fuzzy-sorted descending by semantic version. Pass zero to see all versions.")
			registryArg := cmd.StringOpt("r registry", "", "The docker registry to use")

			cmd.Action = func() {
				registryDomain := ctx.AnkhConfig.Docker.Registry
				if registryArg != nil {
					registryDomain = *registryArg
				}

				output, err := docker.ListImages(ctx, registryDomain, *numToShow)
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

		cmd.Command("create", "Creates a chart directory along with the common files and directories used in a Helm chart", func(cmd *cli.Cmd) {
			cmd.Spec = "[--chart-path] [--starter-chart][--tag-image] [--app-name] [-r]"
			chartPath := cmd.StringOpt("chart-path", "", "The location to create the helm chart, defaults to helm/<app-name> based on directory")
			appName := cmd.StringOpt("app-name", "", "The name to be used for the chart, chart-path overrides this value if both are set")
			starterChart := cmd.StringOpt("starter-chart", "", "The name of the chart in $HELM_HOME/starters/, if not available locally will attempt to pull from remote helm repository")
			tagImage := cmd.StringOpt("tag-image", "", "The name of the docker image, defaults to app-name")
			repositoryArg := cmd.StringOpt("r repository", "", "The chart repository to use")

			cmd.Action = func() {
				ctx.Chart = *starterChart
				err := helm.CreateChart(ctx, *chartPath, *appName, *tagImage, *repositoryArg)
				check(err)
				os.Exit(0)
			}
		})

		cmd.Command("ls", "List Helm charts and their versions", func(cmd *cli.Cmd) {
			cmd.Spec = "[-n] [-r]"
			numToShow := cmd.IntOpt("n num", 5, "Number of versions to show, sorted descending by creation date. Pass zero to see all versions.")
			repositoryArg := cmd.StringOpt("r repository", "", "The chart repository to use")

			cmd.Action = func() {
				repository := ctx.DetermineHelmRepository(repositoryArg)
				helmOutput, err := helm.ListCharts(ctx, repository, *numToShow)
				check(err)
				if helmOutput != "" {
					fmt.Printf(helmOutput)
				}
				os.Exit(0)
			}
		})

		cmd.Command("versions", "List versions for a Helm chart", func(cmd *cli.Cmd) {
			cmd.Spec = "[-r] CHART"
			chart := cmd.StringArg("CHART", "", "The Helm chart to fetch versions for")
			repositoryArg := cmd.StringOpt("r repository", "", "The chart repository to use")

			cmd.Action = func() {
				repository := ctx.DetermineHelmRepository(repositoryArg)
				helmOutput, err := helm.ListVersions(ctx, repository, *chart, false)
				check(err)
				if helmOutput != "" {
					fmt.Println(helmOutput)
				}
				os.Exit(0)
			}
		})

		cmd.Command("inspect", "Inspect a Helm chart", func(cmd *cli.Cmd) {
			cmd.Spec = "[-r] CHART"
			chart := cmd.StringArg("CHART", "", "The Helm chart to inspect, passed in the `CHART[@VERSION]` format.")
			repositoryArg := cmd.StringOpt("r repository", "", "The chart repository to use")

			cmd.Action = func() {
				repository := ctx.DetermineHelmRepository(repositoryArg)
				helmOutput, err := helm.Inspect(ctx, repository, *chart)
				check(err)
				if helmOutput != "" {
					fmt.Println(helmOutput)
				}
				os.Exit(0)
			}
		})

		cmd.Command("publish", "Publish a Helm chart using files from the current directory", func(cmd *cli.Cmd) {
			cmd.Spec = "[-r] [--version]"
			repositoryArg := cmd.StringOpt("r repository", "", "The chart repository to use")
			versionArg := cmd.StringOpt("version", "", "The chart version to publish. Overrides any version present in Chart.yaml")

			cmd.Action = func() {
				repository := ctx.DetermineHelmRepository(repositoryArg)
				err := helm.Publish(ctx, repository, *versionArg)
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
			ctx.SkipConfig = true

			cmd.Action = func() {
				// Use the original, unmerged config. We want to explicitly avoid
				// serializing the contents of any remote configs.
				newAnkhConfig, err := config.GetAnkhConfig(ctx, ctx.AnkhConfigPath)
				if err != nil {
					newAnkhConfig = ankh.AnkhConfig{}
				}

				if len(newAnkhConfig.Contexts) == 0 {
					newAnkhConfig.Contexts = map[string]ankh.Context{
						"minikube": {
							KubeContext:       "minikube",
							EnvironmentClass:  "dev",
							ResourceProfile:   "constrained",
							Release:           "minikube",
							HelmRepositoryURL: "https://kubernetes-charts.storage.googleapis.com",
						},
					}
					ctx.Logger.Infof("Initializing `contexts` to a single sample context for kube-context `minikube`")
				}

				if len(newAnkhConfig.Environments) == 0 {
					newAnkhConfig.Environments = map[string]ankh.Environment{
						"minikube": {
							Contexts: []string{"minikube"},
						},
					}
					ctx.Logger.Infof("Initializing `environments` to a single sample envionment with context `minikube`'")
				}

				out, err := yaml.Marshal(newAnkhConfig)
				check(err)

				err = ioutil.WriteFile(ctx.AnkhConfigPath, out, 0644)
				check(err)

				os.Exit(0)
			}
		})

		cmd.Command("ls", "List current Ankh configuration sources", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				for _, i := range ctx.AnkhConfig.Include {
					fmt.Println(i)
				}

				os.Exit(0)
			}
		})

		cmd.Command("add", "Add an Ankh configuration source", func(cmd *cli.Cmd) {
			ctx.SkipConfig = true

			cmd.Spec = "SOURCE"
			sourceArg := cmd.StringArg("SOURCE", "", "The configuration source to add. May be a local file or a remote HTTP resource.")

			cmd.Action = func() {
				if sourceArg == nil || *sourceArg == "" {
					ctx.Logger.Fatalf("Must provide a configuration source")
				}

				// Use the original, unmerged config. We want to explicitly avoid
				// serializing the contents of any remote configs.
				newAnkhConfig, err := config.GetAnkhConfig(ctx, ctx.AnkhConfigPath)
				check(err)

				for _, include := range newAnkhConfig.Include {
					if *sourceArg == include {
						ctx.Logger.Infof("Configuration source \"%v\" already present in config path \"%v\", nothing to do",
							*sourceArg, ctx.AnkhConfigPath)
						os.Exit(0)
					}
				}

				newAnkhConfig.Include = append(newAnkhConfig.Include, *sourceArg)
				ctx.Logger.Infof("Added configuration source \"%v\" to config path \"%v\"",
					*sourceArg, ctx.AnkhConfigPath)

				out, err := yaml.Marshal(newAnkhConfig)
				check(err)

				err = ioutil.WriteFile(ctx.AnkhConfigPath, out, 0644)
				check(err)

				os.Exit(0)
			}
		})

		cmd.Command("rm", "Remove an Ankh configuration source", func(cmd *cli.Cmd) {
			ctx.SkipConfig = true

			cmd.Spec = "SOURCE"
			sourceArg := cmd.StringArg("SOURCE", "", "The configuration source to remove. May be a local file or a remote HTTP resource.")

			cmd.Action = func() {
				if sourceArg == nil || *sourceArg == "" {
					ctx.Logger.Fatalf("Must provide a configuration source")
				}

				// Use the original, unmerged config. We want to explicitly avoid
				// serializing the contents of any remote configs.
				newAnkhConfig, err := config.GetAnkhConfig(ctx, ctx.AnkhConfigPath)
				check(err)

				found := false
				for i, include := range newAnkhConfig.Include {
					if *sourceArg == include {
						newAnkhConfig.Include = append(newAnkhConfig.Include[:i], newAnkhConfig.Include[i+1:]...)
						ctx.Logger.Infof("Removed configuration source \"%v\" from config path \"%v\"",
							include, ctx.AnkhConfigPath)
						found = true
						break
					}
				}

				if !found {
					ctx.Logger.Infof("Configuration source \"%v\" not found in config path \"%v\", nothing to do",
						*sourceArg, ctx.AnkhConfigPath)
					os.Exit(0)
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
				s := getContextTable(&ctx.AnkhConfig)
				fmt.Printf(strings.Join(s, "\n"))
				os.Exit(0)
			}
		})

		cmd.Command("get-environments", "Get available environments", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				s := getEnvironmentTable(&ctx.AnkhConfig)
				fmt.Printf(strings.Join(s, "\n"))
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

			ctx.Logger.Infof("`%v version --client --short` output:", ctx.AnkhConfig.Helm.Command)
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
