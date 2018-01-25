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

def command_header(action, global_config):
    namespace_context = ""
    if 'namespace' in global_config:
        namespace_context = " and namespace \"" + global_config['namespace'] + "\""
    full_context = "context \"" + global_config['kube_context'] + "\" using environment \"" + global_config['environment'] + "\"" + namespace_context
    print "#"
    print "# %s %s" % (action, full_context)
    print "#"
    print ""
    return


def command_footer(text):
    print ""
    print "#"
    print "# done. %s" % text
    print "#"
    print ""
    return


def stage_print(text):
    print "#"
    print "# %s" % text
    print "#"


def explain_something(args, text):
    if args.explain:
        print "# %s" % text


def flatten(l):
    return [item for sublist in l for item in sublist]


# Takes a dict and collapses it into key=value pairs
# Example:
#     collapse({
#         'one': 1,
#         'two': {
#             'three': 3,
#             'four': {
#                 'five': 5,
#             },
#         },
#         'six': 'six',
#     })
# Becomes
#     ['six=six', 'two.four.five=5', 'two.three=3', 'one=1']
def collapse(x, path=[], acc=[]):
    if isinstance(x, dict):
        return flatten([collapse(v, path + [k.replace('-', '_')], acc) for k, v in x.items()])
    else:
        value = str(x).lower() if isinstance(x, bool) else str(x)

        return acc + ['.'.join(path) + '=' + value]
