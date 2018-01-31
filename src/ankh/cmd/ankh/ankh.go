package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/jawher/mow.cli"
	"github.com/sirupsen/logrus"

	"gopkg.in/yaml.v2"

	"ankh"
	"ankh/helm"
	"ankh/kubectl"
)

var log = logrus.New()

func main() {
	app := cli.App("ankh", "Another Kubernetes Helper")
	app.Spec = "[-v] [--ankhconfig] [--kubeconfig] [--datadir]"

	var (
		verbose    = app.BoolOpt("v verbose", false, "Verbose debug mode")
		ankhconfig = app.StringOpt("ankhconfig", path.Join(os.Getenv("HOME"), ".ankh/config"), "The ankhconfig to use.")
		kubeconfig = app.StringOpt("kubeconfig", "", "The kubeconfig to use when invoking kubectl.")
		datadir    = app.StringOpt("datadir", path.Join(os.Getenv("HOME"), ".ankh"), "The data directory for ankh template history.")
	)

	formatter := logrus.TextFormatter{
		DisableTimestamp: true,
	}
	log.Out = os.Stdout
	log.Formatter = &formatter

	ctx := &ankh.ExecutionContext{}

	app.Before = func() {
		if *verbose {
			log.Level = logrus.DebugLevel
		} else {
			log.Level = logrus.InfoLevel
		}

		ctx = &ankh.ExecutionContext{
			ConfigPath: *ankhconfig,
			DataDir:    *datadir,
			KubeConfig: *kubeconfig,
			Logger:     log,
		}

	}

	app.Command("apply", "Deploy an ankh file to a kubernetes cluster", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f] [--dry-run]"

		filename := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")
		dryRun := cmd.BoolOpt("dry-run", false, "Perform a dry-run and don't actually apply anything to a cluster")

		cmd.Action = func() {
			ankhConfig, err := ankh.GetAnkhConfig(ctx)
			check(err)

			rootAnkhFile, err := ankh.ParseAnkhFile(*filename)
			check(err)

			log.Infof("Using ankh file %v", *filename)

			dependencies := rootAnkhFile.Dependencies
			if len(rootAnkhFile.AdminDependencies) > 0 {
				log.Infof("Found admin dependencies, processing those first...")
				dependencies = append(rootAnkhFile.AdminDependencies, dependencies...)
			}

			for _, dep := range dependencies {
				prev, err := os.Getwd()
				check(err)

				err = os.Chdir(dep)
				check(err)

				// Should this not be hard coded?
				ankhFile, err := ankh.ParseAnkhFile("ankh.yaml")
				check(err)

				log.Infof("Resolving dependency %v", dep)
				helmOutput, err := helm.Template(ctx, ankhFile, ankhConfig)
				check(err)

				kubectlOutput, err := kubectl.Execute(ctx, kubectl.Apply, *dryRun, helmOutput, ankhFile, ankhConfig)
				check(err)

				if *verbose {
					fmt.Println(kubectlOutput)
				}

				err = os.Chdir(prev)
				check(err)
			}

			if len(rootAnkhFile.Charts) > 0 {
				helmOutput, err := helm.Template(ctx, rootAnkhFile, ankhConfig)
				check(err)

				kubectlOutput, err := kubectl.Execute(ctx, kubectl.Apply, *dryRun, helmOutput, rootAnkhFile, ankhConfig)
				check(err)

				if *verbose {
					fmt.Println(kubectlOutput)
				}
			}

			log.Info("Complete")
			os.Exit(0)
		}
	})

	app.Command("template", "Output the results of templating an ankh file", func(cmd *cli.Cmd) {
		cmd.Spec = "[-f]"

		filename := cmd.StringOpt("f filename", "ankh.yaml", "Config file name")

		cmd.Action = func() {
			ankhConfig, err := ankh.GetAnkhConfig(ctx)
			check(err)

			rootAnkhFile, err := ankh.ParseAnkhFile(*filename)
			check(err)

			log.Infof("Using ankh file %v", *filename)

			dependencies := rootAnkhFile.Dependencies
			if len(rootAnkhFile.AdminDependencies) > 0 {
				log.Infof("Found admin dependencies, processing those first...")
				dependencies = append(rootAnkhFile.AdminDependencies, dependencies...)
			}

			for _, dep := range dependencies {
				prev, err := os.Getwd()
				check(err)

				err = os.Chdir(dep)
				check(err)

				// Should this not be hard coded?
				ankhFile, err := ankh.ParseAnkhFile("ankh.yaml")
				check(err)

				log.Infof("Resolving dependency %v", dep)
				helmOutput, err := helm.Template(ctx, ankhFile, ankhConfig)
				check(err)

				fmt.Println(helmOutput)

				err = os.Chdir(prev)
				check(err)
			}

			if len(rootAnkhFile.Charts) > 0 {
				helmOutput, err := helm.Template(ctx, rootAnkhFile, ankhConfig)
				check(err)

				fmt.Println(helmOutput)
			}

			log.Info("Complete")
			os.Exit(0)
		}
	})

	app.Command("config", "Manage ankh configuration", func(cmd *cli.Cmd) {
		cmd.Command("view", "Merge all available configs and show the result", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				ankhConfig, err := ankh.GetAnkhConfig(ctx)
				check(err)

				out, err := yaml.Marshal(ankhConfig)
				check(err)

				fmt.Print(string(out))
				os.Exit(0)
			}
		})

		cmd.Command("use-context", "Switch to a context", func(cmd *cli.Cmd) {
			cmd.Spec = "CONTEXT"

			arg := cmd.StringArg("CONTEXT", "", "")

			cmd.Action = func() {
				ankhConfig, err := ankh.GetAnkhConfig(ctx)
				check(err)

				if *arg == "" {
					log.Error("Missing CONTEXT")
					os.Exit(1)
				}

				context := *arg
				ankhConfig.CurrentContextName = context

				ok := false
				for name, _ := range ankhConfig.Contexts {
					if context == name {
						ok = true
						break
					}
				}
				if !ok {
					log.Errorf("Context \"%v\" not found under `contexts`.", context)
					log.Info("The following contexts are available:")
					for name, _ := range ankhConfig.Contexts {
						log.Infof("- %v", name)
					}
					os.Exit(1)
				}

				out, err := yaml.Marshal(ankhConfig)
				check(err)

				err = ioutil.WriteFile(ctx.ConfigPath, out, 0644)
				check(err)

				fmt.Printf("Switched to context \"%v\".\n", context)
				os.Exit(0)
			}
		})

		cmd.Command("get-contexts", "Get available contexts", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				ankhConfig, err := ankh.GetAnkhConfig(ctx)
				check(err)

				for name, _ := range ankhConfig.Contexts {
					fmt.Println(name)
				}
				os.Exit(0)
			}
		})

		cmd.Command("current-context", "Get the current context", func(cmd *cli.Cmd) {
			cmd.Action = func() {
				ankhConfig, err := ankh.GetAnkhConfig(ctx)
				check(err)

				fmt.Println(ankhConfig.CurrentContextName)
				os.Exit(0)
			}
		})
	})

	app.Run(os.Args)
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
