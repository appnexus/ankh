package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
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

var log = logrus.New()

func execute(ctx *ankh.ExecutionContext) {
	log.Infof("Gathering global configuration...")

	rootAnkhFile, err := ankh.ParseAnkhFile(ctx.AnkhFilePath)
	if err == nil {
		log.Infof("- OK: %v", ctx.AnkhFilePath)
	}
	check(err)

	// run the bootstrap scripts, if they exist
	bootstrapScripts := rootAnkhFile.Bootstrap.Scripts
	if len(bootstrapScripts) > 0 {
		log.Infof("Found bootstrap scripts, executing those now...")
		runScripts(ctx, bootstrapScripts)
	} else {
		log.Infof("`bootstrap` section not found in config. Skipping.");
	}

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
	} else if len(dependencies) == 0 {
		ctx.Logger.Warningf("No charts nor dependencies specified in ankh file %s, nothing to do", ctx.AnkhFilePath)
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

	app.Run(os.Args)
}

func check(err error) {
	if err != nil {
		log.Fatalf("Cannot proceed: %v\n", err)
		os.Exit(1)
	}
}

func runScripts(ctx *ankh.ExecutionContext, scripts []struct { Path string }) {
	for _, script := range scripts {
		path := script.Path
		if path == "" {
			log.Warnf("Missing path in script %s", script)
			break
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
			log.Infof("- OK (dry) %s", path)
			break
		}
		// pass kube context and the "global" config as a yaml environment variable
		cmd := exec.Command(path)
		if ctx.AnkhConfig.CurrentContext.Global != nil {
			global, err := yaml.Marshal(ctx.AnkhConfig.CurrentContext.Global)
			check(err)
			cmd.Env = append(
				os.Environ(),
				"ANKH_CONFIG_GLOBAL=" + string(global),
				"ANKH_KUBE_CONTEXT=" + string(ctx.AnkhConfig.CurrentContext.KubeContext))
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
