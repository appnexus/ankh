# Ankh

Another Kubernetes Helper for shipping code.


## Introduction

**Ankh** is a command line tool that wraps **helm template** and **kubectl apply**.

**Ankh** makes managing multiple Helm charts easy:

```
$ cat ankh.yaml
namespace: mynamespace

charts:
  - name: haste-server
    version: 0.0.1
    default_values:
      tag: latest
      ingress:
        host: haste.myorgization.net
  - name: zoonavigator
    version: 0.0.1
    default_values:
      tag: latest
      ingress:
        host: zoonavigator.dev.myorganization.net
    values:
      production:
        tag: 1.0.0
        ingress:
          host: zoonavigator.prod.myorganization.net
```

There are two operations you can perform: *template* and *apply*.

**ankh template** runs helm template on all of the helm charts and prints the output, keeping any extra logging prefixed with a # to mark it as a yaml comment.

**ankh apply** runs kubectl apply over the templated output of all the helm charts.

## Configuration

### Contexts

**Ankh** config is driven by *contexts*, much like kubectl. When ankh is invoked, it uses the *current-context* to decide which kubectl context, environment, and other common configuration to use.

```
$ cat ~/.ankh/config
current-context: kfc0-production
contexts:
  minikube-local:
    kube_context: minikube
    environment: production
    profile: constrained
    helm_registry_url: https://helm-registry.myorganization.net/helm-repo/charts
    global:
      some_value: needed_by_all_charts
>>>>>>> 65974d0... Better documentation
```

You can view available contexts from your ankh config using:

```
ankh config get-contexts
```

...and switch to one via use-context

```
ankh config use-context
```

The config/context API design was taken straight from kubectl, for a familiar feel.

Full Ankh config schema documentation coming soon.

### Ankh files

Once your ankh config contains the set of contexts and you've selected one via *use-context*, the primary source of input to ankh will be an Ankh file, typically named ankh.yaml

```
$ cat ankh.yaml | head -n15
namespace: utils

charts:
  - name: haste-server
    version: 0.0.1
    default_values:
      tag: latest
      ingress:
        host: haste.myorgization.net
    resource_profiles:
      constrained:
        haste-server:
          cpu: 0.1
          memory: 64Mi
      natural:
```

An Ankh file tracks the target namespace and all of the charts you want to manage.

Full Ankh file schema documentation coming soon.
