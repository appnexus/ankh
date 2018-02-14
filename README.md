# Ankh

Another Kubernetes Helper for shipping code.

## Dependencies
Ankh uses kubectl and helm under the hood, make sure you install them.
Kubectl: https://kubernetes.io/docs/tasks/tools/install-kubectl/
Helm (version 2.7 or newer): https://github.com/kubernetes/helm

## Build and Installation
To build Ankh, just run `make`

To install Ankh simply run `make install`.

## Introduction

**Ankh** is a command line tool that wraps **helm template** and **kubectl apply**.

**Ankh** makes managing multiple Helm charts easy:

```
$ cat ankh.yaml
namespace: mynamespace

charts:
  - name: haste-server
    version: 0.0.1
    default-values:
      tag: latest
      ingress:
        host: haste.myorgization.net
  - name: zoonavigator
    version: 0.0.1
    default-values:
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
current-context: minikube-local

supported-environments:
  - dev
  - production

supported-resource-profiles:
  - natural
  - constrained

contexts:
  minikube-local:
    kube-context: minikube
    environment: dev
    resource-profile: constrained
    helm-registry-url: https://helm-registry.myorganization.net/helm-repo/charts
    global:
      some-value: "needed by all charts"
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
    default-values:
      tag: latest
      ingress:
        host: haste.myorgization.net
    resource-profiles:
      constrained:
        haste-server:
          cpu: 0.1
          memory: 64Mi
      natural:
```

An Ankh file tracks the target namespace and all of the charts you want to manage.

Full Ankh file schema documentation coming soon.
