package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"time"

	"github.com/jawher/mow.cli"
	"github.com/mattn/go-isatty"
	"github.com/sirupsen/logrus"

	"gopkg.in/yaml.v2"

	"ankh"
	"ankh/helm"
	"ankh/kubectl"
	"ankh/util"
)

var log = logrus.New()

func execute(ctx *ankh.ExecutionContext) {
	log.Infof("Gathering global configuration...")

	rootAnkhFile, err := ankh.ParseAnkhFile(ctx.AnkhFilePath)
	if err == nil {
		log.Infof("- OK: %v", ctx.AnkhFilePath)
	}
	check(err)

	dependencies := rootAnkhFile.Dependencies
	if ctx.AnkhConfig.CurrentContext.ClusterAdmin && len(rootAnkhFile.AdminDependencies) > 0 {
		log.Infof("Found admin dependencies, processing those first...")
		dependencies = append(rootAnkhFile.AdminDependencies, dependencies...)
	}

	executeAnkhFile := func(ankhFile ankh.AnkhFile) {
		verb := "Templating"
		if ctx.Apply {
			verb = "Applying"
		}

		ctx.Logger.Infof("%v release \"%v\" with context \"%s\" using environment \"%s\" to namespace \"%s\"", verb,
			ctx.AnkhConfig.CurrentContext.Release,
			ctx.AnkhConfig.CurrentContext.KubeContext,
			ctx.AnkhConfig.CurrentContext.Environment,
			ankhFile.Namespace)

		helmOutput, err := helm.Template(ctx, ankhFile)
		check(err)

		if ctx.Apply {
			kubectlOutput, err := kubectl.Execute(ctx, kubectl.Apply, helmOutput, ankhFile)
			check(err)

			if ctx.Verbose {
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
		ctx.Logger.Infof("Running from directory %v", wd)

		// Should this be configurable?
		path := "ankh.yaml"

		ctx.Logger.Infof("Gathering local configuration...")
		ankhFile, err := ankh.ParseAnkhFile(path)
		if err == nil {
			ctx.Logger.Infof("- OK: %v", path)
		}
		check(err)

		executeAnkhFile(ankhFile)

		err = os.Chdir(prev)
		check(err)
	}

	if len(rootAnkhFile.Charts) > 0 {
		executeAnkhFile(rootAnkhFile)
	}
}

func main() {
	app := cli.App("ankh", "Another Kubernetes Helper")
	app.Spec = "[-v] [--ankhconfig] [--kubeconfig] [--datadir]"

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

		ctx = &ankh.ExecutionContext{
			Verbose:        *verbose,
			AnkhConfigPath: *ankhconfig,
			KubeConfigPath: *kubeconfig,
			DataDir:        path.Join(*datadir, fmt.Sprintf("%v", time.Now().Unix())),
			Logger:         log,
		}

		ankhConfig, err := ankh.GetAnkhConfig(ctx)
		check(err)

		ctx.AnkhConfig = ankhConfig

		log.Debugf("Using KubeConfigPath %v (KUBECONFIG = '%v')", ctx.KubeConfigPath, os.Getenv("KUBECONFIG"))
		log.Debugf("Using AnkhConfigPath %v (ANKHCONFIG = '%v')", ctx.AnkhConfigPath, os.Getenv("ANKHCONFIG"))
	}

	app.Command("apply", "Deploy an ankh file to a kubernetes cluster", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--dry-run]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		dryRun := cmd.BoolOpt("dry-run", false, "Perform a dry-run and don't actually apply anything to a cluster")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath
			ctx.DryRun = *dryRun
			ctx.Apply = true

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("template", "Output the results of templating an ankh file", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f]"

		ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")

		cmd.Action = func() {
			ctx.AnkhFilePath = *ankhFilePath

			execute(ctx)
			os.Exit(0)
		}
	})

	app.Command("inspect", "Inspect charts in ankh.yaml and display information.", func(cmd *cli.Cmd) {

		cmd.Command("values", "For each chart, display contexts of values.yaml, "+
			"ankh-values.yaml, and ankh-resource-profiles.yaml", func(cmd *cli.Cmd) {

			cmd.Spec = "[-f] [--use-context]"
			ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
			useContext := cmd.BoolOpt("use-context", false, "Filter values by current context")

			cmd.Action = func() {
				ctx.AnkhFilePath = *ankhFilePath
				ctx.UseContext = *useContext
				inspect(ctx, helm.InspectValues)
				os.Exit(0)
			}
		})

		cmd.Command("chart", "For each chart, display contents of the Charts.yaml file",
			func(cmd *cli.Cmd) {

				cmd.Spec = "[-f]"
				ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")

				cmd.Action = func() {
					ctx.AnkhFilePath = *ankhFilePath
					inspect(ctx, helm.InspectChart)
					os.Exit(0)
				}
			})

		cmd.Command("templates", "For each chart, display contents of each raw template file",
			func(cmd *cli.Cmd) {

				cmd.Spec = "[-f]"
				ankhFilePath := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")

				cmd.Action = func() {
					ctx.AnkhFilePath = *ankhFilePath
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

				config := ctx.AnkhConfig
				config.CurrentContextName = context

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

	app.Run(os.Args)
}

func inspect(ctx *ankh.ExecutionContext,
	cb func(ctx *ankh.ExecutionContext, chart ankh.Chart, ankhFile ankh.AnkhFile) (string, error)) {
	var result string

	ankhFile, err := ankh.ParseAnkhFile(ctx.AnkhFilePath)
	if err == nil {
		log.Infof("- OK: %v", ctx.AnkhFilePath)
	}
	check(err)

	if len(ankhFile.Charts) > 0 {
		ctx.Logger.Debug("Inspecting charts")
		for _, chart := range ankhFile.Charts {
			output, err := cb(ctx, chart, ankhFile)
			check(err)
			result += output
		}
	}

	fmt.Println(result)
}

func check(err error) {
	if err != nil {
		log.Fatalf("Cannot proceed: %v\n", err)
	}
}
