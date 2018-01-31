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
import tarfile
import tempfile
import yaml

from ankh.utils import explain_something, collapse

logger = logging.getLogger('ankh')

def get_ingress_host_from_config(global_config, name):
    o = global_config['global']['ingress'].get(name, None)
    if o is not None:
        return o['host']
    return None

def get_ingress_host_by_chart_name(global_config, name):
    if 'global' in global_config and 'ingress' in global_config['global']:
        ingresses = global_config['global']['ingress']
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
    for item in collapse(kv):
        helm += ['--set', item]
    return

def helm_append_ingress(helm, chart, global_config, args):
    name = chart['name']
    ingress_host = get_ingress_host_by_chart_name(global_config, name)
    if ingress_host:
        ingress_variable = chart.get('ingress_variable', 'ingress.host')
        helm += [ '--set', '{}={}'.format(ingress_variable, ingress_host) ]
    return

def helm_append_base(helm, global_config):
    helm += [ '--kube-context', global_config['kube_context'] ]
    if 'namespace' in global_config:
        helm += [ '--namespace', global_config['namespace'] ]
    return

def helm_append_values(helm, chart, global_config, args):
    default_values = chart.get('default_values', {})
    if len(default_values.items()) > 0:
        helm_append_kv(helm, default_values, "")
        explain_something(args, "Appending default_values for chart %s found in top-level chart config under 'values:'" % chart['name'])
    else:
        explain_something(args, "No default_values for chart %s found in top-level chart config" % chart['name'])

    values = chart.get('values', {})
    if len(values.items()) > 0:
        if global_config['environment'] in values:
            explain_something(args, "Appending values found in top-level chart config for env %s under 'values:'" % global_config['environment'])
            with tempfile.NamedTemporaryFile(delete=False) as tmp:
                yaml.dump(values[global_config['environment']], tmp, default_flow_style=False)
                explain_something(args, "Appending temp values file %s for env %s" % (tmp.name, global_config['environment']))
                helm += [ '-f', tmp.name ]
    else:
        explain_something(args, "No values for chart %s found in top-level chart config" % chart['name'])

    chartref = chart['chartref']
    if not chartref.endswith('.tgz'):
        return
    with tarfile.open(chartref) as tar:
        tmp_dir = tempfile.mkdtemp()
        tar.extractall(tmp_dir)
        path = '%s/%s/ankh-values.yaml' % (tmp_dir, chart['name'])
        print "checking for path ", path
        if os.path.exists(path):
            with open(path, 'r') as f:
                values = yaml.safe_load(f.read())
            if global_config['environment'] in values:
                with tempfile.NamedTemporaryFile(delete=False) as tmp:
                    yaml.dump(values[global_config['environment']], tmp, default_flow_style=False)
                helm += [ '-f', tmp.name ]
                explain_something(args, "Appending values file %s from extracted chartref %s for env %s" % (tmp.name, chartref, global_config['environment']))
            else:
                logger.warning("environment %s not found in ankh-values.yaml for chartref %s" % (global_config['environment'], chartref))
        else:
            explain_something(args, "No ankh-values.yaml present in chartref %s" % chartref)
    return

def helm_append_secrets(helm, chart, global_config, args):
    secrets = chart.get('secrets', {})
    if len(secrets.items()) > 0:
        if global_config['environment'] in secrets:
            explain_something(args, "Appending secrets found in top-level chart config for env %s under 'secrets:'" % global_config['environment'])
            helm_append_kv(helm, secrets[global_config['environment']], "")
    else:
        explain_something(args, "No secrets found in top-level chart config")
    return

def helm_append_profile(helm, chart, global_config, args):
    resource_profiles = chart.get('resource_profiles', {})
    if len(resource_profiles.items()) > 0:
        if global_config['profile'] in resource_profiles:
            explain_something(args, "Appending resource_profiles found in top-level chart config for env %s under 'resource_profiles:'" % global_config['profile'])
            with tempfile.NamedTemporaryFile(delete=False) as tmp:
                yaml.dump(resource_profiles[global_config['profile']], tmp, default_flow_style=False)
                explain_something(args, "Appending temp resource_profiles file %s for env %s" % (tmp.name, global_config['profile']))
                helm += [ '-f', tmp.name ]
    else:
        explain_something(args, "No resource_profiles for chart %s found in top-level chart config" % chart['name'])

    chartref = chart['chartref']
    if not chartref.endswith('.tgz'):
        return
    with tarfile.open(chartref) as tar:
        tmp_dir = tempfile.mkdtemp()
        tar.extractall(tmp_dir)
        path = '%s/%s/ankh-resource-profiles.yaml' % (tmp_dir, chart['name'])
        print "checking for path ", path
        if os.path.exists(path):
            with open(path, 'r') as f:
                resource_profiles = yaml.safe_load(f.read())
            if global_config['profile'] in resource_profiles:
                with tempfile.NamedTemporaryFile(delete=False) as tmp:
                    yaml.dump(resource_profiles[global_config['profile']], tmp, default_flow_style=False)
                helm += [ '-f', tmp.name ]
                explain_something(args, "Appending resource_profiles file %s from extracted chartref %s for env %s" % (tmp.name, chartref, global_config['profile']))
            else:
                logger.warning("environment %s not found in ankh-resource-profiles.yaml for chartref %s" % (global_config['profile'], chartref))
        else:
            explain_something(args, "No ankh-resource-profiles.yaml present in chartref %s" % chartref)
    return

# Append certain keys that may be used by charts globally. This pattern should
# be avoided unless necessary for many apps globally
#
# The magic `context` top level property that is made available to all charts
# here.
def helm_append_context(helm, chart, global_config, args):
    if 'global' in global_config:
        for item in collapse({'global': global_config['global']}):
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
