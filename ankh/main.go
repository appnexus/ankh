package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/imdario/mergo"
	"github.com/jawher/mow.cli"
	"github.com/mattn/go-isatty"
	"github.com/sirupsen/logrus"

	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	"github.com/appnexus/ankh/config"
	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/helm"
	"github.com/appnexus/ankh/kubectl"
	"github.com/appnexus/ankh/util"
)

var AnkhBuildVersion string = "DEVELOPMENT"

var log = logrus.New()

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

	return strings.Join(filtered, "---")
}

func logExecuteAnkhFile(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile) {
	verb := ""
	switch ctx.Mode {
	case ankh.Apply:
		verb = "Applying"
	case ankh.Explain:
		verb = "Explaining"
	case ankh.Template:
		verb = "Templating"
	case ankh.Lint:
		verb = "Linting"
	}

	releaseLog := ""
	if ctx.AnkhConfig.CurrentContext.Release != "" {
		releaseLog = fmt.Sprintf(" release \"%v\"", ctx.AnkhConfig.CurrentContext.Release)
	}

	dryLog := ""
	if ctx.DryRun {
		dryLog = " (dry run)"
	}

	namespaceLog := ""
	if ankhFile.Namespace != "" {
		namespaceLog = fmt.Sprintf(" to namespace \"%s\"", ankhFile.Namespace)
	}

	contextLog := fmt.Sprintf(" using kube-context \"%v\"", ctx.AnkhConfig.CurrentContext.KubeContext)
	if ctx.AnkhConfig.CurrentContext.KubeServer != "" {
		contextLog = fmt.Sprintf(" to kube-server \"%v\"", ctx.AnkhConfig.CurrentContext.KubeServer)
	}

	ctx.Logger.Infof("%v%v%v%v with environment class \"%v\" and resource profile \"%v\"%v", verb,
		releaseLog, dryLog, contextLog,
		ctx.AnkhConfig.CurrentContext.EnvironmentClass,
		ctx.AnkhConfig.CurrentContext.ResourceProfile,
		namespaceLog)
}

func execute(ctx *ankh.ExecutionContext) {
	log.Infof("Reading Ankh file from: %v", ctx.AnkhFilePath)

	rootAnkhFile, err := ankh.ParseAnkhFile(ctx.AnkhFilePath)
	if err == nil {
		log.Debugf("- OK: %v", ctx.AnkhFilePath)
	}
	check(err)

	contexts := []string{}
	if ctx.Environment != "" {
		environment, ok := ctx.AnkhConfig.Environments[ctx.Environment]
		if !ok {
			log.Errorf("Environment '%v' not found in `environments`", ctx.Environment)
			log.Info("The following environments are available:")
			keys := []string{}
			for k, _ := range ctx.AnkhConfig.Environments {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, name := range keys {
				log.Infof("- %v", name)
			}
			os.Exit(1)
		}

		contexts = environment.Contexts
		log.Infof("Executing over environment \"%v\" with contexts [ %v ]", ctx.Environment, strings.Join(contexts, ", "))

		for _, context := range contexts {
			log.Infof("Beginning to operate on context \"%v\" in environment \"%v\"", context, ctx.Environment)
			switchContext(ctx, &ctx.AnkhConfig, context)
			executeContext(ctx, rootAnkhFile)
			log.Infof("Finished with context \"%v\" in environment \"%v\"", context, ctx.Environment)
		}
	} else {
		if ctx.AnkhConfig.CurrentContextName == "" {
			// Not sure if this is possible actually
			log.Fatalf("No CurrentContextName found. Must provide an explicit --context or --environment")
		}
		executeContext(ctx, rootAnkhFile)
	}
}

func executeContext(ctx *ankh.ExecutionContext, rootAnkhFile ankh.AnkhFile) {

	dependencies := []string{}
	if ctx.Chart == "" {
		dependencies = rootAnkhFile.Dependencies
		if ctx.AnkhConfig.CurrentContext.ClusterAdmin && len(rootAnkhFile.AdminDependencies) > 0 {
			log.Infof("Found admin dependencies, processing those first...")
			dependencies = append(rootAnkhFile.AdminDependencies, dependencies...)
		}
	} else {
		log.Debugf("Skipping dependencies since we are operating only on chart %v", ctx.Chart)
	}

	executeAnkhFile := func(ankhFile ankh.AnkhFile) {
		switch ctx.Mode {
		case ankh.Apply:
			fallthrough
		case ankh.Explain:
			// run the bootstrap scripts, if they exist
			bootstrapScripts := ankhFile.Bootstrap.Scripts
			if len(bootstrapScripts) > 0 {
				log.Info("Bootstrapping...")
				runScripts(ctx, bootstrapScripts)
			}
		}

		logExecuteAnkhFile(ctx, ankhFile)

		if ctx.HelmVersion == "" {
			ver, err := helm.Version()
			if err != nil {
				ctx.Logger.Fatalf("Failed to get helm version info: %v", err)
			}
			ctx.HelmVersion = ver
			ctx.Logger.Debug("Using helm version: ", strings.TrimSpace(ver))
		}

		helmOutput, err := helm.Template(ctx, ankhFile)
		check(err)

		if len(ctx.Filters) > 0 {
			helmOutput = filterOutput(ctx, helmOutput)
		}

		switch ctx.Mode {
		case ankh.Apply:
			fallthrough
		case ankh.Explain:
			if ctx.KubectlVersion == "" {
				ver, err := kubectl.Version()
				if err != nil {
					ctx.Logger.Fatalf("Failed to get kubectl version info: %v", err)
				}
				ctx.KubectlVersion = ver
				ctx.Logger.Debug("Using kubectl version: ", strings.TrimSpace(ver))
			}

			kubectlOutput, err := kubectl.Execute(ctx, helmOutput, ankhFile, nil)
			check(err)

			if ctx.Mode == ankh.Explain {
				// Sweet string badnesss.
				helmOutput = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(helmOutput), "&& \\"))
				fmt.Println(fmt.Sprintf("(%s) | \\\n%s", helmOutput, kubectlOutput))
			} else if ctx.Verbose {
				fmt.Println(kubectlOutput)
			}
		case ankh.Template:
			fmt.Println(helmOutput)
		case ankh.Lint:
			errors := helm.Lint(ctx, helmOutput, ankhFile)
			if len(errors) > 0 {
				for _, err := range errors {
					ctx.Logger.Warningf("%v", err)
				}
				log.Fatalf("Lint found %d errors.", len(errors))
			}

			ctx.Logger.Infof("No issues.")
		}
	}

	for _, dep := range dependencies {
		log.Infof("Satisfying dependency: %v", dep)

		prev, err := os.Getwd()
		check(err)

		err = os.Chdir(dep)
		check(err)

		wd, _ := os.Getwd()
		ctx.Logger.Debugf("Running from directory %v", wd)

		// Should this be configurable?
		path := "ankh.yaml"

		ctx.Logger.Debugf("Gathering local configuration...")
		ankhFile, err := ankh.ParseAnkhFile(path)
		if err == nil {
			ctx.Logger.Debugf("- OK: %v", path)
		}
		check(err)

		executeAnkhFile(ankhFile)

		err = os.Chdir(prev)
		check(err)
	}

	if len(rootAnkhFile.Charts) > 0 {
		executeAnkhFile(rootAnkhFile)
	} else if len(dependencies) == 0 {
		ctx.Logger.Warningf("No charts nor dependencies specified in ankh file %s, nothing to do", ctx.AnkhFilePath)
	}
}

func checkContext(ankhConfig *ankh.AnkhConfig, context string) {
	_, ok := ankhConfig.Contexts[context]
	if !ok {
		log.Errorf("Context '%v' not found in `contexts`", context)
		log.Info("The following contexts are available:")
		keys := []string{}
		for k, _ := range ankhConfig.Contexts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, name := range keys {
			log.Infof("- %v", name)
		}
		os.Exit(1)
	}
}

func switchContext(ctx *ankh.ExecutionContext, ankhConfig *ankh.AnkhConfig, context string) {
	checkContext(ankhConfig, context)

	errs := ankhConfig.ValidateAndInit(ctx, context)
	if len(errs) > 0 {
		if ctx.WarnOnConfigError {
			for _, s := range errs {
				log.Warnf("%v", s)
			}
		} else if !ctx.IgnoreConfigError {
			// The config validation errors are not recoverable.
			log.Fatalf("%v", util.MultiErrorFormat(errs))
		}
	}
}

func main() {
	app := cli.App("ankh", "Another Kubernetes Helper")
	app.Spec = "[-v] [--ankhconfig] [--kubeconfig] [--datadir] [--context] [--environment] [--set...]"

	var (
		verbose    = app.BoolOpt("v verbose", false, "Verbose debug mode")
		ankhconfig = app.String(cli.StringOpt{
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
		context = app.String(cli.StringOpt{
			Name:   "context",
			Value:  "",
			Desc:   "The context to use. Must provide this, or a whole environment via --environment",
			EnvVar: "ANKHCONTEXT",
		})
		environment = app.String(cli.StringOpt{
			Name:  "environment",
			Value: "",
			Desc:  "The environment to use. Must provide this, or an individual context via --context",
		})
		datadir = app.String(cli.StringOpt{
			Name:   "datadir",
			Value:  path.Join(os.Getenv("HOME"), ".ankh", "data"),
			Desc:   "The data directory for ankh template history",
			EnvVar: "ANKHDATADIR",
		})
		helmSet = app.Strings(cli.StringsOpt{
			Name:  "set",
			Desc:  "Global variables passed to helm with helm --set, will override variables set in ankhconfig global",
			Value: []string{},
		})
	)

	log.Out = os.Stdout
	log.Formatter = &util.CustomFormatter{
		IsTerminal: isatty.IsTerminal(os.Stdout.Fd()),
	}

	ctx := &ankh.ExecutionContext{}

	app.Before = func() {
		if *verbose {
			log.Level = logrus.DebugLevel
		} else {
			log.Level = logrus.InfoLevel
		}

		helmVars := map[string]string{}
		for _, helmkvPair := range *helmSet {
			k := strings.Split(helmkvPair, "=")
			if len(k) != 2 {
				log.Debugf("Malformed helm set value '%v', skipping...", helmkvPair)
			} else {
				helmVars[k[0]] = k[1]
			}
		}

		if *context != "" && *environment != "" {
			log.Fatalf("Must not provide both --context and --environment, because an environment maps to one or more contexts.")
		}

		ctx = &ankh.ExecutionContext{
			Verbose:           *verbose,
			AnkhConfigPath:    *ankhconfig,
			KubeConfigPath:    *kubeconfig,
			ContextOverride:   *context,
			Environment:       *environment,
			DataDir:           path.Join(*datadir, fmt.Sprintf("%v", time.Now().Unix())),
			Logger:            log,
			HelmSetValues:     helmVars,
			WarnOnConfigError: ctx.WarnOnConfigError,
			IgnoreConfigError: ctx.IgnoreConfigError,
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

			ankhConfig, err := config.GetAnkhConfig(ctx, configPath)
			if err != nil {
				if ctx.WarnOnConfigError {
					for _, s := range strings.Split(err.Error(), "\n") {
						log.Warnf("%v: %v", configPath, s)
					}
				} else if !ctx.IgnoreConfigError {
					// The config validation errors are not recoverable.
					check(err)
				}
			}

			// Merge it in. We'll need to dedup arrays later.
			mergo.Merge(&mergedAnkhConfig, ankhConfig)

			// Follow includes, mark this one as visited.
			configPaths = append(configPaths, ankhConfig.Include...)
			parsedConfigs[configPath] = true
		}

		mergedAnkhConfig.Include = util.ArrayDedup(mergedAnkhConfig.Include)
		mergedAnkhConfig.SupportedEnvironmentClasses = util.ArrayDedup(mergedAnkhConfig.SupportedEnvironmentClasses)
		mergedAnkhConfig.SupportedResourceProfiles = util.ArrayDedup(mergedAnkhConfig.SupportedResourceProfiles)

		if ctx.ContextOverride != "" {
			mergedAnkhConfig.CurrentContextName = ctx.ContextOverride
		}
		if ctx.Environment == "" {
			log.Debugf("Switching to context %v", mergedAnkhConfig.CurrentContextName)
			switchContext(ctx, &mergedAnkhConfig, mergedAnkhConfig.CurrentContextName)
		}
		ctx.AnkhConfig = mergedAnkhConfig
	}

	app.Command("explain", "Explain how an ankh file would be applied to a Kubernetes cluster", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the explain command to only the specified chart")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.Chart = *chart
			ctx.Mode = ankh.Explain

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("apply", "Deploy an ankh file to a Kubernetes cluster", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--dry-run] [--chart] [--filter...]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		dryRun := cmd.BoolOpt("dry-run", false, "Perform a dry-run and don't actually apply anything to a cluster")
		chart := cmd.StringOpt("chart", "", "Limits the apply command to only the specified chart")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action.")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = *dryRun
			ctx.Chart = *chart
			ctx.Mode = ankh.Apply
			filters := []string{}
			for _, filter := range *filter {
				filters = append(filters, string(filter))
			}
			ctx.Filters = filters

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("lint", "Lint an ankh file, checking for possible errors or mistakes", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart] [--filter...]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the lint command to only the specified chart")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action.")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.Chart = *chart
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

	app.Command("template", "Output the results of templating an ankh file", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart] [--filter...]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the template command to only the specified chart")
		filter := cmd.StringsOpt("filter", []string{}, "Kubernetes object kinds to include for the action. The entries in this list are case insensitive. Any object whose `kind:` does not match this filter will be excluded from the action.")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.Chart = *chart
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

	app.Command("inspect", "Inspect charts in ankh.yaml and display information", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart]"
		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the inspect command to only the specified chart")

		cmd.Command("values", "For each chart, display contents of values.yaml, "+
			"ankh-values.yaml, and ankh-resource-profiles.yaml", func(cmd *cli.Cmd) {
			cmd.Spec += " [--use-context]"
			useContext := cmd.BoolOpt("use-context", false, "Select values by current context")

			cmd.Action = func() {
				ctx.AnkhFilePath = *ankhFilePath
				ctx.UseContext = *useContext
				ctx.Chart = *chart
				if ctx.Environment != "" {
					log.Fatalf("Must not provide --environment to inspect, because inspect operates on charts using a single context.")
				}
				inspect(ctx, helm.InspectValues)
				os.Exit(0)
			}
		})

		cmd.Command("chart", "For each chart, display contents of the Charts.yaml file",
			func(cmd *cli.Cmd) {
				cmd.Action = func() {
					ctx.AnkhFilePath = *ankhFilePath
					ctx.Chart = *chart
					inspect(ctx, helm.InspectChart)
					os.Exit(0)
				}
			})

		cmd.Command("templates", "For each chart, display contents of each raw template file",
			func(cmd *cli.Cmd) {
				cmd.Action = func() {
					ctx.AnkhFilePath = *ankhFilePath
					ctx.Chart = *chart
					inspect(ctx, helm.InspectTemplates)
					os.Exit(0)
				}
			})
	})

	app.Command("config", "Manage ankh configuration", func(cmd *cli.Cmd) {
		ctx.WarnOnConfigError = true

		cmd.Command("init", "Initialize ankh configuration", func(cmd *cli.Cmd) {
			// Sloppy
			ctx.WarnOnConfigError = false
			ctx.IgnoreConfigError = true

			cmd.Action = func() {
				if len(ctx.AnkhConfig.SupportedEnvironmentClasses) == 0 {
					ctx.AnkhConfig.SupportedEnvironmentClasses = []string{"dev"}
					ctx.Logger.Infof("Initializing `supported-environment-classes`: %v", ctx.AnkhConfig.SupportedEnvironmentClasses)
				}

				if len(ctx.AnkhConfig.SupportedResourceProfiles) == 0 {
					ctx.AnkhConfig.SupportedResourceProfiles = []string{"constrained"}
					ctx.Logger.Infof("Initializing `supported-resource-profiles`: %v", ctx.AnkhConfig.SupportedResourceProfiles)
				}

				if ctx.AnkhConfig.CurrentContextName == "" {
					ctx.AnkhConfig.CurrentContextName = "minikube"
					ctx.Logger.Infof("Initializing `current-context`: %v", ctx.AnkhConfig.CurrentContextName)
				}

				if len(ctx.AnkhConfig.Contexts) == 0 {
					ctx.AnkhConfig.Contexts = map[string]ankh.Context{
						"minikube": {
							KubeContext:      "minikube",
							EnvironmentClass: "dev",
							ResourceProfile:  "constrained",
							HelmRegistryURL:  "https://kubernetes-charts.storage.googleapis.com",
							ClusterAdmin:     true,
						},
					}
					ctx.Logger.Infof("Initializing `contexts` to a single sample context for kube-context `minikube`")
				}

				out, err := yaml.Marshal(ctx.AnkhConfig)
				check(err)

				err = ioutil.WriteFile(ctx.AnkhConfigPath, out, 0644)
				check(err)

				os.Exit(0)
			}
		})

		cmd.Command("view", "View merged ankh configuration", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				out, err := yaml.Marshal(ctx.AnkhConfig)
				check(err)

				fmt.Print(string(out))
				os.Exit(0)
			}
		})

		cmd.Command("use-context", "Switch to a context", func(cmd *cli.Cmd) {
			cmd.Spec = "CONTEXT"

			arg := cmd.StringArg("CONTEXT", "", "")

			cmd.Action = func() {
				if *arg == "" {
					log.Error("Missing CONTEXT")
					os.Exit(1)
				}

				context := *arg

				// Read + modify + write the local config only.
				// We do not want to serialize anything included from a remote config.
				body, err := ioutil.ReadFile(ctx.AnkhConfigPath)
				check(err)

				ankhConfig := ankh.AnkhConfig{}
				err = yaml.UnmarshalStrict(body, &ankhConfig)
				check(err)

				checkContext(&ankhConfig, context)
				ankhConfig.CurrentContextName = context

				out, err := yaml.Marshal(ankhConfig)
				check(err)

				err = ioutil.WriteFile(ctx.AnkhConfigPath, out, 0644)
				check(err)

				fmt.Printf("Switched to context \"%v\".\n", context)
				os.Exit(0)
			}
		})

		cmd.Command("get-contexts", "Get available contexts", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				w := tabwriter.NewWriter(os.Stdout, 0, 8, 8, ' ', 0)
				fmt.Fprintf(w, "NAME\tRELEASE\tENVIRONMENT-CLASS\tRESOURCE-PROFILE\n")
				keys := []string{}
				for k, _ := range ctx.AnkhConfig.Contexts {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, name := range keys {
					ctx, _ := ctx.AnkhConfig.Contexts[name]
					fmt.Fprintf(w, "%v\t%v\t%v\t%v\n", name, ctx.Release, ctx.EnvironmentClass, ctx.ResourceProfile)
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
					fmt.Fprintf(w, "%v\t%v\n", name, strings.Join(env.Contexts, ","))
				}
				w.Flush()
				os.Exit(0)
			}
		})

		cmd.Command("current-context", "Get the current context", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				if ctx.Environment != "" {
					log.Fatalf("Must not provide --environment to current-context, because an environment maps to one or more contexts.")
				}
				fmt.Println(ctx.AnkhConfig.CurrentContextName)
				os.Exit(0)
			}
		})
	})

	app.Command("version", "Show version info", func(cmd *cli.Cmd) {
		ctx.WarnOnConfigError = true

		cmd.Action = func() {
			ctx.Logger.Infof("Ankh version info:")
			fmt.Println(AnkhBuildVersion)

			ctx.Logger.Infof("`helm version --client` output:")
			ver, err := helm.Version()
			check(err)
			fmt.Print(ver)

			ctx.Logger.Infof("`kubectl version --client` output:")
			ver, err = kubectl.Version()
			check(err)
			fmt.Print(ver)

			os.Exit(0)
		}
	})

	app.Run(os.Args)
}

func inspect(ctx *ankh.ExecutionContext,
	cb func(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile, chart ankh.Chart) (string, error)) {
	var result string

	ankhFile, err := ankh.ParseAnkhFile(ctx.AnkhFilePath)
	check(err)

	if len(ankhFile.Charts) < 1 {
		log.Infof(
			"%s does not contain any charts. Inspect commands only operate on ankh.yaml files containing charts",
			ctx.AnkhFilePath)
		return
	}

	ctx.Logger.Debug("Inspecting charts")
	for _, chart := range ankhFile.Charts {
		if ctx.Chart != "" && chart.Name != ctx.Chart {
			continue
		}

		output, err := cb(ctx, ankhFile, chart)
		check(err)
		result += output
	}

	if result == "" {
		ctx.Logger.Fatalf("Chart %s was specified with `--chart` but does not exist in the charts array", ctx.Chart)
		return
	}

	fmt.Println(result)
}

func check(err error) {
	if err != nil {
		log.Fatalf("%v", err)
	}
}

func runScripts(ctx *ankh.ExecutionContext, scripts []struct{ Path string }) {
	for _, script := range scripts {
		path := script.Path
		if path == "" {
			log.Warnf("Missing path in script %s", script)
			break
		}
		if !filepath.IsAbs(path) {
			cwd, err := os.Getwd()
			if err != nil {
				log.Fatalf("Failed to get working directory when attempting to run script %s", script)
			}
			path = filepath.Join(cwd, path)
		}
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			log.Fatalf("Script not found: %s", path)
		}
		// check that the script is executable
		err = syscall.Access(path, unix.X_OK)
		if err != nil {
			log.Fatalf("Permission denied, script is not executable: %s", path)
		}
		log.Infof("Running script: %s", path)
		if ctx.DryRun {
			log.Infof("- OK (dry run) %s", path)
			break
		}
		// pass kube context and the "global" config as a yaml environment variable
		cmd := exec.Command(path)
		envVars := []string{}
		if ctx.AnkhConfig.CurrentContext.Global != nil {
			global, err := yaml.Marshal(ctx.AnkhConfig.CurrentContext.Global)
			check(err)
			envVars = []string{
				"ANKH_CONFIG_GLOBAL=" + string(global),
				"ANKH_KUBE_CONTEXT=" + string(ctx.AnkhConfig.CurrentContext.KubeContext),
				"KUBECONFIG=" + string(ctx.KubeConfigPath),
			}
		}
		if ctx.Mode == ankh.Explain {
			explainVars := []string{}
			for _, s := range envVars {
				replaced := strings.Replace(s, "\n", "\\n", -1)
				eqIdx := strings.Index(replaced, "=")
				eVar := fmt.Sprintf("export %v='%v' &&", replaced[0:eqIdx], replaced[eqIdx+1:])
				explainVars = append(explainVars, eVar)
			}
			explain := strings.Join(explainVars, " ")
			explain += " "
			explain += strings.Join(cmd.Args, " ")
			fmt.Println(explain)
			break
		}
		if len(envVars) > 0 {
			cmd.Env = append(os.Environ(), envVars...)
		}
		var stdOut, stdErr bytes.Buffer
		cmd.Stdout = &stdOut
		cmd.Stderr = &stdErr
		err = cmd.Run()
		if err != nil {
			log.Fatalf("- FAILED %s:\n%s\nstdout: %s\nstderr: %s", path, err, stdOut.String(), stdErr.String())
		}
		log.Infof("- OK %s", path)
		log.Debugf("%s Stdout:\n%s", path, stdOut.String())
	}
}
