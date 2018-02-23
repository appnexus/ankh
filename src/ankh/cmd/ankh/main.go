package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jawher/mow.cli"
	"github.com/mattn/go-isatty"
	"github.com/sirupsen/logrus"

	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v2"

	"ankh"
	"ankh/helm"
	"ankh/kubectl"
	"ankh/util"
)

var AnkhBuildVersion string

var log = logrus.New()

func logExecuteAnkhFile(ctx *ankh.ExecutionContext, ankhFile ankh.AnkhFile) {
	verb := "Templating"
	if ctx.Apply {
		verb = "Applying"
	} else if ctx.Explain {
		verb = "Explaining"
	}

	releaseLog := ""
	if ctx.AnkhConfig.CurrentContext.Release != "" {
		releaseLog = fmt.Sprintf("release \"%v\" ", ctx.AnkhConfig.CurrentContext.Release)
	}

	dryLog := ""
	if ctx.DryRun {
		dryLog = "(dry run) "
	}

	namespaceLog := ""
	if ankhFile.Namespace != "" {
		namespaceLog = fmt.Sprintf(" to namespace \"%s\"", ankhFile.Namespace)
	}

	ctx.Logger.Infof("%v %v%vwith context \"%s\" using environment \"%s\"%v", verb,
		releaseLog, dryLog,
		ctx.AnkhConfig.CurrentContext.KubeContext,
		ctx.AnkhConfig.CurrentContext.Environment,
		namespaceLog)
}

func execute(ctx *ankh.ExecutionContext) {
	log.Infof("Reading Ankh file from: %v", ctx.AnkhFilePath)

	rootAnkhFile, err := ankh.ParseAnkhFile(ctx.AnkhFilePath)
	if err == nil {
		log.Debugf("- OK: %v", ctx.AnkhFilePath)
	}
	check(err)

	dependencies := rootAnkhFile.Dependencies
	if ctx.AnkhConfig.CurrentContext.ClusterAdmin && len(rootAnkhFile.AdminDependencies) > 0 {
		log.Infof("Found admin dependencies, processing those first...")
		dependencies = append(rootAnkhFile.AdminDependencies, dependencies...)
	}

	executeAnkhFile := func(ankhFile ankh.AnkhFile) {
		if ctx.Apply || ctx.Explain {
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
				ctx.Logger.Fatalf("Failed to get helm version info: please ensure helm is installed (version 2.7.X or greater) and accessible from your $PATH")
			}
			ctx.HelmVersion = ver
			ctx.Logger.Debug("Using helm version: ", strings.TrimSpace(ver))
		}

		helmOutput, err := helm.Template(ctx, ankhFile)
		check(err)

		if ctx.Apply || ctx.Explain {
			if ctx.KubectlVersion == "" {
				ver, err := kubectl.Version()
				if err != nil {
					ctx.Logger.Fatalf("Failed to get kubectl version info: please ensure kubectl is installed and accessible from your $PATH")
				}
				ctx.KubectlVersion = ver
				ctx.Logger.Debug("Using kubectl version: ", strings.TrimSpace(ver))
			}

			kubectlOutput, err := kubectl.Execute(ctx, kubectl.Apply, helmOutput, ankhFile, nil)
			check(err)

			if ctx.Explain {
				// Sweet string badnesss.
				helmOutput = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(helmOutput), "&& \\"))
				fmt.Println(fmt.Sprintf("(%s) | \\\n%s", helmOutput, kubectlOutput))
			} else if ctx.Verbose {
				fmt.Println(kubectlOutput)
			}
		} else {
			fmt.Println(helmOutput)
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

func main() {
	app := cli.App("ankh", "Another Kubernetes Helper")
	app.Spec = "[-v] [--ankhconfig] [--kubeconfig] [--datadir] [--set...]"

	var (
		verbose    = app.BoolOpt("v verbose", false, "Verbose debug mode")
		ankhconfig = app.String(cli.StringOpt{
			Name:   "ankhconfig",
			Value:  path.Join(os.Getenv("HOME"), ".ankh", "config"),
			Desc:   "The ankh config to use",
			EnvVar: "ANKHCONFIG",
		})
		kubeconfig = app.String(cli.StringOpt{
			Name:   "kubeconfig",
			Value:  path.Join(os.Getenv("HOME"), ".kube/config"),
			Desc:   "The kube config to use when invoking kubectl",
			EnvVar: "KUBECONFIG",
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

		ctx = &ankh.ExecutionContext{
			Verbose:        *verbose,
			AnkhConfigPath: *ankhconfig,
			KubeConfigPath: *kubeconfig,
			DataDir:        path.Join(*datadir, fmt.Sprintf("%v", time.Now().Unix())),
			Logger:         log,
			HelmSetValues:  helmVars,
		}

		ankhConfig, err := ankh.GetAnkhConfig(ctx)
		check(err)

		ctx.AnkhConfig = ankhConfig

		log.Debugf("Using KubeConfigPath %v (KUBECONFIG = '%v')", ctx.KubeConfigPath, os.Getenv("KUBECONFIG"))
		log.Debugf("Using AnkhConfigPath %v (ANKHCONFIG = '%v')", ctx.AnkhConfigPath, os.Getenv("ANKHCONFIG"))
	}

	app.Command("explain", "Explain how an ankh file would be applied to a kubernetes cluster", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the explain command to only the specified chart")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.Chart = *chart
			ctx.Explain = true

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("apply", "Deploy an ankh file to a kubernetes cluster", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--dry-run] [--chart]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		dryRun := cmd.BoolOpt("dry-run", false, "Perform a dry-run and don't actually apply anything to a cluster")
		chart := cmd.StringOpt("chart", "", "Limits the apply command to only the specified chart")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = *dryRun
			ctx.Chart = *chart
			ctx.Apply = true

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("template", "Output the results of templating an ankh file", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the template command to only the specified chart")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.Chart = *chart

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("inspect", "Inspect charts in ankh.yaml and display information.", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--chart]"
		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		chart := cmd.StringOpt("chart", "", "Limits the inspect command to only the specified chart")

		cmd.Command("values", "For each chart, display contents of values.yaml, "+
			"ankh-values.yaml, and ankh-resource-profiles.yaml", func(cmd *cli.Cmd) {

			cmd.Spec += " [--use-context]"
			useContext := cmd.BoolOpt("use-context", false, "Filter values by current context")

			cmd.Action = func() {
				ctx.AnkhFilePath = *ankhFilePath
				ctx.UseContext = *useContext
				ctx.Chart = *chart
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
		cmd.Command("view", "Merge all available configs and show the result", func(cmd *cli.Cmd) {
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

				ok := false
				for name, _ := range ctx.AnkhConfig.Contexts {
					if context == name {
						ok = true
						break
					}
				}
				if !ok {
					log.Errorf("Context \"%v\" not found under `contexts`.", context)
					log.Info("The following contexts are available:")
					for name, _ := range ctx.AnkhConfig.Contexts {
						log.Infof("- %v", name)
					}
					os.Exit(1)
				}

				ctx.AnkhConfig.CurrentContextName = context

				out, err := yaml.Marshal(ctx.AnkhConfig)
				check(err)

				err = ioutil.WriteFile(ctx.AnkhConfigPath, out, 0644)
				check(err)

				fmt.Printf("Switched to context \"%v\".\n", context)
				os.Exit(0)
			}
		})

		cmd.Command("get-contexts", "Get available contexts", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				for name, _ := range ctx.AnkhConfig.Contexts {
					fmt.Println(name)
				}
				os.Exit(0)
			}
		})

		cmd.Command("current-context", "Get the current context", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				fmt.Println(ctx.AnkhConfig.CurrentContextName)
				os.Exit(0)
			}
		})
	})

	app.Command("version", "Show version info", func(cmd *cli.Cmd) {
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
		log.Fatalf("Cannot proceed: %v\n", err)
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
			}
		}
		if ctx.Explain {
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
