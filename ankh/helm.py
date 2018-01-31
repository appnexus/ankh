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
import logging
import subprocess
import sys
import yaml

from ankh.utils import explain_something, collapse

logger = logging.getLogger('ankh')

def get_ingress_host_from_config(global_config, name):
    o = global_config['context']['ingress'].get(name, None)
    if o is not None:
        return o['host']
    return None

def get_ingress_host_by_chart_name(global_config, name):
    if 'context' in global_config and 'ingress' in global_config['context']:
        ingresses = global_config['context']['ingress']
        if name in ingresses:
            o = ingresses[name]
            if type(o) is str:
                return o
            return o['host']
    return None

def fetch_chart(global_config, name, version, args):
    path = '%s-%s.tgz' % (name, version)
    fullpath = 'charts/%s' % path

    if not os.path.exists('charts/'):
        os.mkdir('charts', 0755)

    # Eventually, 'helm fetch' instead of curling the registry.
    cmd = 'curl --max-time 1 --retry 5 --retry-delay 1 -s -k %s/%s > %s' % (global_config['helm_registry_url'], path, fullpath)
    proc = subprocess.Popen([cmd], shell=True)

    proc.wait()
    if proc.returncode != 0:
        logger.error("- FAIL: %s" % cmd)
        return None

    if args.verbose:
        logger.info("- OK: %s" % cmd)
    else:
        logger.info("- OK: %s" % fullpath)

    return fullpath

def inspect_chart(chart, args):
    helm = [
        'helm', 'inspect', 'chart'
    ]

    helm_append_chartref(helm, chart)

    logger.debug("Inspecting chart, executing %s" % " ".join(helm))

    proc = subprocess.Popen(helm, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    proc.wait()
    if proc.returncode != 0:
        return None

    result = yaml.safe_load(proc.stdout.read())
    logger.debug("* chartref %s == %s" % (chart['chartref'], result))
    chart['name'] = result['name']
    chart['version'] = result['version']
    return

def helm_append_kv(helm, kv, prefix):
    for key, value in kv.items():
        fullkey = prefix + key
        if type(value) is not dict:
            helm += [ '--set', str(fullkey) + '=' + str(value) ]
        else:
            helm_append_kv(helm, value, fullkey + ".")
    return

def helm_append_ingress(helm, chart, global_config, args):
    name = chart['name']
    ingress_host = get_ingress_host_by_chart_name(global_config, name)
    if ingress_host:
        ingress_variable = chart.get('ingress_variable', 'ingress.host')
        helm += [ '--set', '{}={}'.format(ingress_variable, ingress_host) ]
    return

def get_ref_path(args, directory, sub_directory, chart, ref_var):
    # ref override
    ref = chart.get(ref_var)
    if ref is not None:
        explain_something(args, "Searching for file using override ref in %s/%s for chart %s with ref_var %s" % (directory, sub_directory, chart['name'], ref_var))
        return '{}/{}/{}'.format(directory, sub_directory, ref)

    # sub_directory/chart-name
    ref = '{}/{}/{}.yaml'.format(directory, sub_directory, chart['name'])
    if os.path.exists(ref):
        explain_something(args, "Searching for file under %s/%s for chart %s" % (directory, sub_directory, chart['name']))
        return ref
    else:
        explain_something(args, "No file under %s/%s for chart %s, defaulting to base case" % (directory, sub_directory, chart['name']))

    # chart-name base case
    explain_something(args, "Searching for file using base case path %s/%s.yaml" % (directory, chart['name']))
    return '{}/{}.yaml'.format(directory, chart['name'])

def get_config_file_path(args, env, chart):
    return get_ref_path(args, 'values', env, chart, 'config_ref')

def get_secret_file_path(args, env, chart):
    return get_ref_path(args, 'secrets', env, chart, 'secret_ref')

def get_profile_file_path(args, profile, chart):
    return get_ref_path(args, 'profiles', profile, chart, 'profile_ref')

def helm_append_base(helm, global_config):
    helm += [ '--kube-context', global_config['kube_context'] ]
    if 'namespace' in global_config:
        helm += [ '--namespace', global_config['namespace'] ]
    return

def helm_append_values(helm, chart, global_config, args):
    # Inject chart default values file
    path = 'values.yaml'
    if os.path.exists(path):
        helm += [ '-f', path ]
        explain_something(args, "Selected top-level values file %s" % path)

    # Inject chart-specific values file
    config_file_path = get_config_file_path(args, global_config["environment"], chart)
    if os.path.exists(config_file_path):
        helm += [ '-f', config_file_path ]
        explain_something(args, "Selected values for chart %s from path %s" % (chart['name'], config_file_path))
    else:
        explain_something(args, "No values file for chart %s at path %s" % (chart['name'], config_file_path))

    values = chart.get('values', {})
    if len(values.items()) > 0:
        helm_append_kv(helm, values, "")
        explain_something(args, "Appending values for chart %s found in top-level chart config under 'values:'" % chart['name'])
    else:
        explain_something(args, "No values for chart %s found in top-level chart config" % chart['name'])
    return

def helm_append_secrets(helm, chart, global_config, args):
    # Inject chart default values file
    path = 'secrets.yaml'
    if os.path.exists(path):
        helm += [ '-f', path ]
        explain_something(args, "Selected top-level secrets file %" % path)

    # Inject chart-specific secrets file
    secret_file_path = get_secret_file_path(args, global_config["environment"], chart)
    if os.path.exists(secret_file_path):
        helm += [ '-f', secret_file_path ]
        explain_something(args, "Selected secrets for chart %s from path %s" % (chart['name'], secret_file_path))
    else:
        explain_something(args, "No secrets file for chart %s at path %s" % (chart['name'], secret_file_path))

    secrets = chart.get('secrets', {})
    if len(secrets.items()) > 0:
        helm_append_kv(helm, secrets, "")
        explain_something(args, "Appending secrets found in top-level chart config under 'secrets:'")
    else:
        explain_something(args, "No secrets found in top-level chart config")
    return

def helm_append_profile(helm, chart, global_config, args):
    profile_file_path = get_profile_file_path(args, global_config['profile'], chart)
    if os.path.exists(profile_file_path):
        helm += [ '-f', profile_file_path ]
        explain_something(args, "Selected profile for chart %s from path %s" % (chart['name'], profile_file_path))
    else:
        explain_something(args, "No profile file for chart %s at path %s" % (chart['name'], profile_file_path))
    return


# Append certain keys that may be used by charts globally. This pattern should
# be avoided unless necessary for many apps globally
#
# The magic `context` top level property that is made available to all charts
# here.
def helm_append_context(helm, chart, global_config, args):
    if 'context' in global_config:
        for item in collapse({'context': global_config['context']}):
            helm += ['--set', item]
            explain_something(args, "Appending %s since it is specified on the global config context property" % item)

    return

def helm_append_chartref(helm, chart):
    chartref = chart['chartref']
    helm += [ chartref ]
    return

def helm_append_release(helm, chart, global_config):
    if 'release' in global_config and global_config['release']:
        release = global_config['release']
        helm += [ '--name', release ]
    return

def helm_template_command(chart, global_config, args):
    helm = [ 'helm', 'template' ]
    helm_append_base(helm, global_config)
    helm_append_context(helm, chart, global_config, args)
    helm_append_ingress(helm, chart, global_config, args)
    helm_append_secrets(helm, chart, global_config, args)
    helm_append_values(helm, chart, global_config, args)
    helm_append_profile(helm, chart, global_config, args)
    helm_append_release(helm, chart, global_config)
    helm_append_chartref(helm, chart)
    return helm
