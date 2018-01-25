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

import argparse
import collections
import os.path
import subprocess
import socket
import sys
import yaml
import logging

from ankh.utils import command_header
from ankh.utils import stage_print
from ankh.kubectl import kubectl_action

logger = logging.getLogger('ankh')

def bootstrap_command(global_config, args):
    command_header("bootstrapping", global_config)

    if 'bootstrap' in global_config:
        bootstrap(global_config['bootstrap'], global_config, args)
    else:
        logger.info("bootstrap: not configured, doing nothing")

    return 0

def teardown_command(global_config, args):
    command_header("tearing down", global_config)

    stage_print("running teardown scripts...")

    if 'teardown' in global_config:
        config = global_config['teardown']
        scripts = config.get('scripts')
        run_scripts("teardown", scripts, global_config, dry_run=args.dry_run, verbose=args.verbose)
    else:
        logger.info("no scripts to run")

    return 0

# Bootstrap and deploy
def spinup_command(global_config, args):
    command_header("spinning up", global_config)

    logger.info("Starting bootstrap stage")
    if 'bootstrap' in global_config:
        bootstrap(global_config['bootstrap'], global_config, args)
    else:
        logger.info("Bootstrap stage not configured, skipping")
    logger.info("Bootstrap stage finished")

    logger.info("Starting deploy stage")
    if 'deploy' in global_config:
        kubectl_action('apply', global_config['deploy'], args, None, global_config)
    else:
        logger.info("Deploy stage not configured, doing nothing")
    logger.info("Deploy stage finished")

    return 0


def delete_command(global_config, args):
    command_header("deleting", global_config)
    if 'deploy' in global_config:
        kubectl_action('delete', global_config['deploy'], args, ['--ignore-not-found'], global_config)
    else:
        logger.info("deploy: not configured, doing nothing")

    return

def deploy_command(global_config, args):
    command_header("deploying", global_config)
    if 'deploy' in global_config:
        kubectl_action('apply', global_config['deploy'], args, None, global_config)
    else:
        logger.info("deploy: not configured, doing nothing")
    return

def template_command(global_config, args):
    command_header("templating", global_config)
    if 'deploy' in global_config:
        kubectl_action('template', global_config['deploy'], args, None, global_config)
    else:
        logger.info("deploy: not configured, doing nothing")

    return

def bootstrap(config, global_config, args):
    scripts = config.get('scripts', [])
    run_scripts("bootstrap", scripts, global_config, dry_run=args.dry_run, verbose=args.verbose)

def run_scripts(log_prefix, scripts, global_config, dry_run=False, verbose=False):
    for script in scripts:
        if 'path' not in script:
            logger.info("%s: missing path in script %s" % (log_prefix, script))
            continue

        # Pass two things to each script: the kube context (so they may run kubectl) and
        # the "context" subsection of the global config, which has shared configuration.
        os.environ['ANKH_KUBE_CONTEXT'] = global_config['kube_context']
        if 'context' in global_config:
            os.environ['ANKH_CONFIG_CONTEXT'] = yaml.dump(global_config['context'])

        path = script['path']
        script_command = [
                path,
                global_config['kube_context']
        ]
        logger.info("Running script: %s" % " ".join(script_command))

        if dry_run:
            logger.info("** OK (DRY) %s:" % path)
            continue

        proc = subprocess.Popen(script_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        proc.wait()
        if proc.returncode == 0:
            if verbose:
                logger.debug("** %s: OK\n%s" % (path, proc.stdout.read()))
            else:
                logger.info("** %s: OK" % path)
        else:
            logger.info("** FAILED %s: stdout: %s, stderr: %s" % (path, proc.stdout.read(), proc.stderr.read()))
    return

def merge_config(target, source):
    for key, val in source.iteritems():
        if isinstance(val, collections.Mapping):
            merged = merge_config(target.get(key, { }), val)
            target[key] = merged
        elif isinstance(val, list):
            target[key] = (target.get(key, []) + val)
        else:
            target[key] = source[key]
    return target

def template_ingress_hosts(global_config):
    # If we find the magic macro in any ingress, substitue with the real hostname.
    macro = '${HOSTNAME}'
    hostname = socket.gethostname()

    for app, host in global_config['context']['ingress'].items():
        if type(host) is not str:
            continue
        if host.find(macro) != -1:
            global_config['context']['ingress'][app] = host.replace(macro, hostname)
    return

def main():
    # We prepend a hash to each log line so that the output of this utility
    # can be more easily composed with other scripts, since our logging will
    # look like comments.
    formatter = logging.Formatter('# ankh: %(levelname)s - %(message)s')
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(formatter)
    logger.addHandler(handler)
    logger.setLevel(logging.INFO)

    parser = argparse.ArgumentParser()
    parser.add_argument(dest='command', default='spinup',
            choices=['bootstrap', 'delete', 'deploy', 'spinup', 'teardown', 'template'])
    parser.add_argument('--yes', '-y', action='store_true', default=False)
    parser.add_argument('--verbose', '-v', action='store_true', default=False)
    parser.add_argument('--dry-run', dest='dry_run', action='store_true', default=False)
    parser.add_argument('--explain', dest='explain', action='store_true', default=False)
    parser.add_argument('--chart', dest='chart',
            help='apply the action to only this chart. may be a directory, tgz, or a name from the config file')
    parser.add_argument('--helm-registry', dest='helm_registry', help='the helm registry to use for charts')
    parser.add_argument('--config-file', '-f', action='append', dest='config_files', help='the config file.', default=["deploy.yaml"])
    parser.add_argument('--kube-context', dest='kube_context', help='the kube context to use.')
    parser.add_argument('--kubeconfig', dest='kubeconfig', help='the kube config to use.')
    parser.add_argument('--environment', dest='environment', choices=['autoenv', 'production'], help='the environment to use. files from values/{env} and secrets/{env} will be used.')
    parser.add_argument('--profile', dest='profile', choices=['production', 'minikube'], help='the profile to use. files from profiles/{profile} will be used. takes precedence over files used by "environment"')
    args = parser.parse_args()

    global_config = {}

    logger.info("Running from directory %s" % os.getcwd())
    for config_file in args.config_files:
        if os.path.exists(config_file):
            logger.info("Using config file " + config_file)
            with open(config_file, 'r') as f:
                config = yaml.load(f)
                merge_config(global_config, config)
        elif config_file == 'deploy.yaml':
            logger.info("Ignoring missing default config file " + config_file)
        else:
            logger.error("Cannot find config file " + config_file)
            sys.exit(1)

    # Kube context is optional on the command line. If present, overrides config. Required overall.
    if args.kube_context:
        global_config['kube_context'] = args.kube_context
    if 'kube_context' not in global_config or not global_config['kube_context']:
        logger.error("Must provide kube_context in the config file or on the command line via --kube-context")
        sys.exit(1)

    # Environment is optional on the command line. If present, overrides config. Required overall.
    if args.environment:
        global_config['environment'] = args.environment
    if 'environment' not in global_config or not global_config['environment']:
        logger.error("Must provide environment in the config file or on the command line via --environment")
        sys.exit(1)

    # Profile is optional on the command line. If present, overrides config. Required overall.
    if args.profile:
        global_config['profile'] = args.profile
    if 'profile' not in global_config or not global_config['profile']:
        logger.error("Must provide profile in the config file or on the command line via --profile")
        sys.exit(1)

    # Helm registry is optional on the command line. If present, overrides config. Required overall.
    if args.helm_registry:
        global_config['helm_registry'] = args.helm_registry
    if 'helm_registry' not in global_config or not global_config['helm_registry']:
        logger.error("Must provide helm_registry in the config file or on the command line via --helm-registry")
        sys.exit(1)

    if args.kubeconfig:
        logger.info("Using kubeconfig " + args.kubeconfig)

    # Possibly run some hacky substitutions on each ingress host
    template_ingress_hosts(global_config)

    # Chart can be a singleton  on the command line
    if args.chart and 'deploy' not in global_config:
        global_config['deploy'] = { 'charts': [ { 'chartref': args.chart } ] }

    if args.verbose:
        logger.setLevel(logging.DEBUG)
        logger.info('Using configuration ' + str(global_config))

    try:
        # Only run the bootstrap config section
        if args.command == 'bootstrap':
            return bootstrap_command(global_config, args)

        # Only run the deploy config section. Uses kubectl delete. Possibly contrained to a single chart by args.chart
        if args.command == 'delete':
            return delete_command(global_config, args)

        # Only run the deploy config section. Uses kubectl apply. Possibly contrained to a single chart by args.chart
        if args.command == 'deploy':
            return deploy_command(global_config, args)

        # Run the bootstrap and deploy config sections. Possibly contrained to a single chart by args.chart
        if args.command == 'spinup':
            return spinup_command(global_config, args)

        # Only run the teardown config section. Possibly constrained to a single chart by args.chart
        if args.command == 'teardown':
            return teardown_command(global_config, args)

        # Only print the templated helm charts. Possibly constrainted to a single chart by args.chart
        if args.command == 'template':
            return template_command(global_config, args)
    except KeyboardInterrupt:
        logger.info("Interrupted")
        sys.exit(1)

    logger.error("Unknown command %s" % args.command)
    return 1

