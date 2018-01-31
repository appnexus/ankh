package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/jawher/mow.cli"
	"github.com/sirupsen/logrus"
	"github.com/mattn/go-isatty"

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
	if len(rootAnkhFile.AdminDependencies) > 0 {
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
		ankhconfig = app.StringOpt("ankhconfig", path.Join(os.Getenv("HOME"), ".ankh/config"), "The ankhconfig to use.")
		kubeconfig = app.StringOpt("kubeconfig", "", "The kubeconfig to use when invoking kubectl.")
		datadir    = app.StringOpt("datadir", path.Join(os.Getenv("HOME"), ".ankh"), "The data directory for ankh template history.")
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
			Verbose:    *verbose,
			ConfigPath: *ankhconfig,
			DataDir:    *datadir,
			KubeConfig: *kubeconfig,
			Logger:     log,
		}

		ankhConfig, err := ankh.GetAnkhConfig(ctx)
		check(err)

		ctx.AnkhConfig = ankhConfig

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

				err = ioutil.WriteFile(ctx.ConfigPath, out, 0644)
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

func check(err error) {
	if err != nil {
		log.Fatalf("Cannot proceed: %v\n", err)
		os.Exit(1)
	}
}
