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
import colorlog
import difflib
import os.path
import subprocess
import socket
import sys
import yaml
import logging

from ankh.utils import command_header
from ankh.kubectl import kubectl_action

logger = logging.getLogger('ankh')
valid_commands = [ 'apply', 'template', 'config view', 'config get-contexts', 'config current-context', 'config use-context' ]

def ankh_config_read(args, log=False):
    # Kube context may be present in this user's ankh config. Check for it.
    if os.path.exists(args.ankhconfig):
        with open(args.ankhconfig, 'r') as f:
            config = yaml.safe_load(f)
            if 'current-context' in config:
                current_context = config['current-context']
                if 'contexts' not in config or current_context not in config['contexts']:
                    if log:
                        logger.warning("Current context %s not found under `contexts`. Ignoring." % current_context)
                else:
                    contexts = config['contexts']
                    subconfig = contexts[current_context]
                    return current_context, subconfig
            elif log:
                logger.warning("No current-context key found in ANKHCONFIG. Ignoring.")
    elif log:
        logger.warning("No ANKHCONFIG found at %s. Skipping." % args.ankhconfig)
    return "", None

def current_context_command(args):
    current_context, _ = ankh_config_read(args)
    print current_context
    return 0

def get_contexts(args):
    # Kube context may be present in this user's ankh config. Check for it.
    if os.path.exists(args.ankhconfig):
        with open(args.ankhconfig, 'r') as f:
            config = yaml.safe_load(f)
            if 'contexts' in config:
                return config['contexts']
    return []

def get_contexts_command(args):
    contexts = get_contexts(args)
    for ctx in contexts:
        print ctx

def use_context_command(args, new_context):
    # Kube context may be present in this user's ankh config. Check for it.
    if os.path.exists(args.ankhconfig):
        with open(args.ankhconfig, 'r') as f:
            config = yaml.safe_load(f)
        if 'contexts' not in config or new_context not in config['contexts']:
            logger.error("Context \"%s\" not found under `contexts`." % new_context)
            logger.info("The following contexts are available:")
            contexts = get_contexts(args)
            for ctx in contexts:
                logger.info("- %s" % ctx)
            return 1
        config['current-context'] = new_context
        with open(args.ankhconfig, 'w') as f:
            yaml.dump(config, f, default_flow_style=False)
    elif log:
        logger.warning("No ANKHCONFIG found at %s. Skipping." % args.ankhconfig)

    print "Switched to context \"%s\"." % new_context
    return 0

# bootstrap and apply
def apply_command(global_config, args):
    if 'bootstrap' in global_config:
        command_header("Bootstrapping", global_config)
        bootstrap(global_config['bootstrap'], global_config, args)
    else:
        logger.debug("`bootstrap` section not found in config. Skipping.")

    if 'charts' in global_config:
        command_header("Deploying", global_config)
        kubectl_action('apply', global_config['charts'], args, None, global_config)
    else:
        logger.warning("`charts` section not found in config. Nothing to do. Run `ankh config` to see the config that will be used.")
    return 0

def template_command(global_config, args):
    if 'charts' in global_config:
        command_header("Templating", global_config)
        kubectl_action('template', global_config['charts'], args, None, global_config)
    else:
        logger.warning("`charts` section not found in config. Nothing to do. Run `ankh config view` to see the config that will be used.")
    return 0

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
            logger.info("- OK (dry)%s" % path)
            continue

        proc = subprocess.Popen(script_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        proc.wait()
        if proc.returncode == 0:
            if verbose:
                logger.debug("- OK %s:\n%s" % (path, proc.stdout.read()))
            else:
                logger.info("- OK %s" % path)
        else:
            logger.info("- FAILED %s:\nstdout: %s\nstderr: %s" % (path, proc.stdout.read(), proc.stderr.read()))
    return

def merge_config(target, source):
    if not source:
        return target
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

def gather_config(args, log=False):
    global_config = {}
    # Kube context may be present in this user's ankh config. Check for it.
    current_context, subconfig = ankh_config_read(args, log=True)
    if subconfig is not None:
        logger.debug("current-context is %s" % current_context)
        merge_config(global_config, subconfig)
    for config_file in args.config_files:
        if os.path.exists(config_file):
            if log:
                logger.info("- OK: %s" % config_file)
            with open(config_file, 'r') as f:
                config = yaml.safe_load(f)
                merge_config(global_config, config)
        elif config_file != 'ankh.yaml':
            if args.verbose:
                logger.debug("Ignoring missing default config file " + config_file)
        else:
            logger.error("Cannot find config file " + config_file)
            sys.exit(1)
    return global_config

def main():
    # We prepend a hash to each log line so that the output of this utility
    # can be more easily composed with other scripts, since our logging will
    # look like comments.
    if sys.stdout.isatty():
        log_format = "# %(log_color)s%(levelname)-8s%(reset)s %(message)s"
        formatter = colorlog.ColoredFormatter(
            log_format,
            datefmt=None,
            reset=True,
            log_colors={
                'DEBUG':    'cyan',
                'INFO':     'green',
                'WARNING':  'yellow',
                'ERROR':    'red',
                'CRITICAL': 'red,bg_white',
            },
            secondary_log_colors={},
            style='%'
        )
        handler = colorlog.StreamHandler(sys.stdout)
    else:
        log_format = "# %(levelname)-8s %(message)s"
        formatter = logging.Formatter(log_format, datefmt=None)
        handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(formatter)
    logger.addHandler(handler)
    logger.setLevel(logging.INFO)

    parser = argparse.ArgumentParser()
    parser.add_argument(dest='command', default=['template'], nargs='*',
            help='the command to run. Valid commands are %s ]' % str(valid_commands))
    parser.add_argument('--yes', '-y', action='store_true', default=False)
    parser.add_argument('--verbose', '-v', action='store_true', default=False)
    parser.add_argument('--dry-run', dest='dry_run', action='store_true', default=False)
    parser.add_argument('--explain', dest='explain', action='store_true', default=False)
    parser.add_argument('--chart', dest='chart',
            help='apply the action to only this chart. may be a directory, tgz, or a name from the config file')
    parser.add_argument('--helm-registry-url', dest='helm_registry_url', help='the full helm registry URL to use for charts')
    parser.add_argument('--config-file', '-f', action='append', dest='config_files', help='the config files.', default=['ankh.yaml'])
    parser.add_argument('--kube-context', dest='kube_context', help='the kube context to use.')
    parser.add_argument('--release', dest='release', help='the release to target.')
    parser.add_argument('--kubeconfig', dest='kubeconfig', help='the kube config to use.')
    parser.add_argument('--ankhconfig', dest='ankhconfig', help='the ankh config to use.',
            default=os.environ.get("ANKHCONFIG", os.environ.get("HOME", "") + "/.ankh/config"))
    args = parser.parse_args()

    # context commands require no global_config nor any of the optional command-line args.
    try:
        command = args.command[0]
        if command == 'config':
            if len(args.command) < 2:
                logger.error("need at least 2 arguments for the `config` subcommand")
                sys.exit(1)
            subcommand = args.command[1]
            if subcommand == 'view':
                global_config = gather_config(args, log=True)
                yaml.dump(global_config, sys.stdout, default_flow_style=False)
                return 0
            if subcommand == 'current-context':
                return current_context_command(args)
            if subcommand == 'get-contexts':
                return get_contexts_command(args)
            if subcommand == 'use-context':
                if len(args.command) != 3:
                    logger.error("use-context subcommand requires an argument")
                    sys.exit(1)
                ctx_arg = args.command[2]
                return use_context_command(args, ctx_arg)
    except KeyboardInterrupt:
        logger.info("Interrupted")
        sys.exit(1)

    # We need the first pass of config to determine dependencies.
    logger.info("Gathering global configuration...")
    global_config = gather_config(args, log=True)

    dependencies = []
    if 'admin_dependencies' in global_config:
        if global_config.get('cluster_admin', False):
            logger.debug("Current context has cluster_admin: adding admin_dependencies")
            dependencies.extend(global_config['admin_dependencies'])
        else:
            logger.debug("Current context does not have cluster_admin: skipping admin_dependencies")
    if 'dependencies' in global_config:
        dependencies.extend(global_config['dependencies'])

    if len(dependencies) == 0:
        # common-case: only run in this directory
        return run('.', args)

    # Satisfy each dependency by changing into that directory and running.
    # Recursive dependencies are prevented in run().
    logger.debug("Found dependencies: %s" % str(dependencies))
    for dep in dependencies:
        logger.info("Satisfying dependency: %s" % dep)
        old_dir = os.getcwd()
        os.chdir(dep)
        r = run(dep, args)
        os.chdir(old_dir)
        if r != 0:
            return r
    return 0

def run(base_dir, args):
    logger.info("Running from directory %s" % os.getcwd())

    logger.info("Gathering local configuration...")
    global_config = gather_config(args, log=True)
    if 'dependencies' in global_config:
        logger.error("Base directory %s contains dependencies in its config, but we're processing dependencies found in the global config right now. Recursive dependencies aren't supported, so eliminate them before proceeding." % os.getcwd())
        return 1

    if 'kube_context' not in global_config or not global_config['kube_context']:
        logger.error("Must provide kube_context in the config file or on the command line via --kube-context")
        sys.exit(1)

    if 'environment' not in global_config or not global_config['environment']:
        logger.error("Must provide environment in the config file or on the command line via --environment")
        sys.exit(1)

    if 'profile' not in global_config or not global_config['profile']:
        logger.error("Must provide profile in the config file or on the command line via --profile")
        sys.exit(1)

    # Release is optional on the command line. If present, overrides config. Optional.
    if args.release:
        global_config['release'] = args.release

    # Helm registry is optional on the command line. If present, overrides config. Required overall.
    if args.helm_registry_url:
        global_config['helm_registry_url'] = args.helm_registry_url
    if 'helm_registry_url' not in global_config or not global_config['helm_registry_url']:
        logger.error("Must provide helm_registry_url in the config file or on the command line via --helm-registry")
        sys.exit(1)

    if args.kubeconfig:
        logger.info("Using kubeconfig " + args.kubeconfig)

    # Possibly run some hacky substitutions on each ingress host
    template_ingress_hosts(global_config)

    if args.verbose:
        logger.setLevel(logging.DEBUG)
        logger.info('Using configuration ' + str(global_config))

    try:
        command = args.command[0]
        if command == 'apply':
            return apply_command(global_config, args)
        if command == 'template':
            return template_command(global_config, args)
    except KeyboardInterrupt:
        logger.info("Interrupted")
        sys.exit(1)

    logger.error("Unknown command: '%s'" % command)
    suggestion = difflib.get_close_matches(command, valid_commands, n=1)
    if suggestion and suggestion[0]:
        logger.info("Did you mean '%s'?" % suggestion[0])
    return 1

