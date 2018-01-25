"""
Copyright 2018 AppNexus Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
"""

import os
import sys
import logging
import subprocess

from ankh.utils import stage_print
from ankh.helm import fetch_chart
from ankh.helm import inspect_chart
from ankh.helm import helm_template_command

logger = logging.getLogger('ankh')

def kubectl_append_base(kubectl, global_config, args):
    kubectl += [ '--context', global_config['kube_context'] ]
    if 'namespace' in global_config:
        kubectl += [ '--namespace', global_config['namespace'] ]
    if args.dry_run:
        kubectl += [ '--dry-run' ]
    return

def kubectl_append_extra_args(kubectl, extra_args):
    if extra_args is not None:
        kubectl += extra_args
    return

def kubectl_append_path(kubectl):
    kubectl += [ '-f', '-' ]
    return

def kubectl_append_kubeconfig(kubectl, args):
    if args.kubeconfig:
        kubectl += [ '--kubeconfig', args.kubeconfig ]
    return

def kubectl_action_command(action, global_config, args, extra_args):
    kubectl = [ 'kubectl', action ]
    kubectl_append_kubeconfig(kubectl, args)
    kubectl_append_base(kubectl, global_config, args)
    kubectl_append_extra_args(kubectl, extra_args)
    kubectl_append_path(kubectl)
    return kubectl

# args = cli args
def kubectl_chart_action_commands(action, chart, args, extra_args, global_config):
    helm = helm_template_command(chart, global_config, args)
    if action == 'template':
        kubectl = [ 'cat' ]
    else:
        kubectl = kubectl_action_command(action, global_config, args, extra_args)
    return chart['chartref'], helm, kubectl

# args = cli args
def kubectl_action(action, config, args, extra_args, global_config):
    targets = []
    if args.chart and ((args.chart.endswith('.tgz') and os.path.isfile(args.chart)) or os.path.isdir(args.chart)):
        # args.chart, if provided, is the chartref
        chart = { 'chartref': args.chart }
        # we need to inspect the chartref to get name and version. this is a haphazard design.
        inspect_chart(chart, args)
        return kubectl_action_targets(action, config, [ chart ], args, extra_args, global_config)

    for chart in config['charts']:
        name = chart.get('name', None)
        if name is None:
            logger.error("Invalid chart: missing name")
            continue

        if args.chart and name != args.chart:
            continue

        if 'chartref' in chart:
            targets.append(chart)
            continue

        version, name = chart['version'], chart['name']
        path = fetch_chart(global_config, name, version, args)
        if path is None:
            logger.error("Failed to fetch chart %s with version %s" % (name, version))
            return -1

        chart['chartref'] = path
        targets.append(chart)

    if args.chart and len(targets) == 0:
        logger.error("could not find any target for chart arg " + args.chart)
        return -1

    return kubectl_action_targets(action, config, targets, args, extra_args, global_config)


# args = cli args
# Here we assume that targets is a list of charts, each with a chartref propery.
def kubectl_action_targets(action, config, targets, args, extra_args, global_config):
    if not args.yes:
        print "# Executing action '%s' on targets: %s" % (action, " ".join(map(lambda x: x['name'], targets)))
        ok = raw_input("okay? y/N > ")
        if ok != 'y' and ok != 'Y':
            print "# aborting"
            sys.exit(1)

    procs = []
    for chart in targets:
        name, helm, kubectl = kubectl_chart_action_commands(action, chart, args, extra_args, global_config)
        helm_command = " ".join(helm)
        kubectl_command = " ".join(kubectl)
        if args.explain:
            print helm_command, "|", kubectl_command
        else:
            logger.debug("Running Helm command: %s" % helm_command)
            logger.debug("Running kubectl command: %s" % kubectl_command)
            hproc = subprocess.Popen(helm, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            kproc = subprocess.Popen(kubectl, stdout=subprocess.PIPE, stderr=subprocess.PIPE, stdin=hproc.stdout)
            procs.append((name, hproc, kproc))

    stage_print("Waiting for processes...")

    for path, hproc, kproc in procs:
        # wait for kubectl, if any, and print the result
        kout = kproc.stdout.read()
        kerr = kproc.stderr.read()
        kproc.wait()
        if kproc.returncode == 0:
            if action == 'template':
                print kout
            else:
                if args.verbose:
                    logger.info("** %s: kubectl OK\n%s" % (path, kout))
                else:
                    logger.info("** %s: kubectl OK" % path)
        else:
            logger.error("** %s: kubectl FAILED: %s" % (path, kerr))
            sys.exit(1)
        # wait for helm, print the result, and possibly stdout too.
        herr = hproc.stderr.read()
        hproc.wait()
        if hproc.returncode == 0:
            logger.info("** %s: helm OK" % path)
        else:
            logger.error("** %s: helm FAILED: %s" % (path, herr))
            sys.exit(1)
