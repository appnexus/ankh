# Ankh [![Build Status](https://travis-ci.org/appnexus/ankh.svg?branch=master)](https://travis-ci.org/appnexus/ankh)

<img src="ankh.png" width="175" height="175">
Another Kubernetes Helper for shipping code.

## Dependencies
Ankh uses kubectl and helm under the hood, make sure you install them.
Kubectl: https://kubernetes.io/docs/tasks/tools/install-kubectl/
Helm (version 2.7 or newer): https://github.com/kubernetes/helm

## Build and Installation

### Using `make`
```
make # builds to ankh/ankh
```
```
make install # installs to $GOPATH/bin
```

### Using `go get`
```
go get github.com/appnexus/ankh/ankh
```

## Introduction

**Ankh** is a command line tool that wraps **helm template** and **kubectl apply**.

Ankh helps organizations manage multiple application deployments across different namespaces. Users get to manage their deployments using Helm charts, but without the additional complexity of running Tiller.

Multiple application deployments may be expressed in a single file:

```
$ cat ankh.yaml
namespace: mynamespace

charts:
  - name: haste-server
    version: 0.0.1
    default-values:
      some-value: "that you need"
      more-values:
        x: 1
        y: true
  - name: zoonavigator
    version: 0.0.1
    values:
      production:
        foo: 'very prod'
      dev:
        foo: 'not so prod'
        bar: false       
```

Simplicity, transparency and composability are the primary design goals of Ankh.

**ankh inspect** lets you inspect helm chart files, helm templates, and ankh-derived yaml values.

**ankh template** runs `helm template` with all derived yaml values, prefixing any logging output with a comment `#` to ensure the output is still valid yaml.

**ankh explain** outputs a bash-compatible representation of the underlying invocations to `helm template` and `kubectl apply`.

**ankh apply** runs `kubectl apply` using the `helm template` output.

Ankh makes it easy to observe and verify incremental changes. It can be used to achieve reproducible deployments when combined with source control, or even simple CI / CD pipelines.

## Configuration

### Contexts

**Ankh** configs are driven by *contexts*, much like kubectl. When Ankh is invoked, it uses the *current-context* to decide which kubectl context, environment-class, and other common configurations to use.

```
$ cat ~/.ankh/config
current-context: minikube
contexts:
  minikube-local:
    kube-context: minikube
    environment-class: production
    resource-profile: constrained
    helm-registry-url: https://helm-registry.myorganization.net/helm-repo/charts
    global:
      ingress:
        host: localhost
      some-value: 'needed by all charts'
```

You can view available contexts from your ankh config using:

```
ankh config get-contexts
```

...and switch to one via use-context

```
ankh config use-context my-context
```

You can also specify the context to use via a command line flag:
```
ankh --context my-context apply
```

The config/context API design was taken straight from kubectl, for a familiar feel.

You may include other yaml config files into your ankh config via the `include` property. This is useful if you need to keep multiple ankh configs in sync, perhaps across multiple developers on a team. E.g.

```
$ cat ~/.ankh/config

include:
- https://some-config-server.net/production.yaml
- https://some-config-server.net/staging.yaml
- https://some-config-server.net/development.yaml

...
```

Note: you may only `use-context` on contexts defined inside your ankh config, not any contexts from `include`. Just use `--context` or `--environment` for those.

### Environments
Environments are a list of context names. Using an environment is good for when you have multiple contexts to `apply` to as part of one atomic action. These must be valid context names present under `contexts`. A good example of this would be multiple geo-distributed clusters that you want to deploy to as part of a "staging" environment:

```
environments:
  staging:
    contexts:
    - nym-staging
    - ams-staging
    - lax-staging
```

When you invoke `ankh apply` with the `--environment` flag, it will do an `ankh apply` to each of the contexts defined in the environment in the order listed. E.g.

```
ankh --environment staging apply
```

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
        host: haste.myorganization.net
    resource-profiles:
      constrained:
        haste-server:
          cpu: 0.1
          memory: 64Mi
      natural:
```

An Ankh file tracks the target namespace and all of the charts you want to manage.

## YAML schemas

#### `AnkhConfig`

| Field         |Type| Description   |
| ------------- |:---:|:-------------:| 
| include       |[]string| A list of ankh config references to load and merge into this ankh config. Can be a local file, an http endpoint, or, experimentaly, a Kubernetes ConfigMap reference of the form `kubecontext://$context/$namespace/$object/$key`.|
| environments  |map[string]`Environment`| A mapping from environment name to `Environment` objects. Helps organize Context objects as logical environments for the purpose of operating on many contexts at once.|
| contexts      |map[string]`Context`| A mapping from context names to `Context` objects. Analogous, but not equivalent, to contexts in a kubeconfig.|
| current-context      |string| The current context. This context will be used when Ankh is invoked. Must be a valid context, which is a key under `contexts`. | 
| supported-environments|[]string| Deprecated name for `supported-environment-classes`.|
| supported-environment-classes|[]string| An array of supported environment classes. Any `environment-class` value in a `Context` must be included in this array.|
| supported-resource-profiles|[]string| An array of supported resource profiles. Any `resource-profile` value in a `Context` must be included in this array.|

#### `Environment`
| Field         |Type| Description   |
| ------------- |:---:|:-------------:| 
|contexts|[]string| A list of contexts to that belong to this Environment. These must be valid context names present under `contexts`.|

#### `Context`
| Field         |Type| Description   |
| ------------- |:---:|:-------------:| 
|kube-context|string|The kube context to use. This must be a valid context name present in your kube config (tyipcally ~/.kube/config or $KUBECONFIG)|
|kube-server|string|The kube server to use. This must be a valid Kubernetes API server. Similar to the `server` field in kubectl's `cluster` object. This can be used in place of `kube-context`.)|
|environment|string|Deprecated name for `environment-class`. Not to be confused with `environments` (sets of contexts) present the AnkhConfig object.|
|environment-class|string|The environment class to use. Must be a valid environment class in `supported-environment-classes`|
|resource-profile|string|The resource profile to use. Must be a valid resource profile in `supported-resource-profiles`|
|release|string|The release name to pass to Helm via --release|
|helm-registry-url|string|The URL to the Helm chart repo to use|
|cluster-admin|bool|If true, then `admin-dependencies` are run.|
|global|`Global`|global configuration available to all charts|

#### `Global`
| Field         |Type| Description   |
| ------------- |:---:|:-------------:| 
| ingress       |map[string]string|Map from chart name to ingress host name. The ingress host name is exposed to helm charts as the yaml key `ingress.host`|
| _             |RawYaml|All other keys are provided as raw yaml, each key prefixed with `global.` (eg: `global.somekey` for `somekey` under `Global`)

#### `AnkhFile`
| Field         |Type| Description   |
| ------------- |:---:|:-------------:| 
| namespace     |string|The namespace to use when running `helm` and `kubectl`|
| bootstrap     |`Script`|Optional. A bootstrap script to run before applying any charts.|
| admin-dependencies|[]string|Optional. Path to dependent directories, each containing an ankh.yaml that should be run, in order. These dependencies are only satisified when `cluster-admin` is true in the current `Context`, and they are always run before regular `dependencies`|
| dependencies     |[]string|Optional. Path to dependent directories, each containing an ankh.yaml that should be run, in order.|

#### `Script`
| Field         |Type| Description   |
| ------------- |:---:|:-------------:| 
| path          |string|The path to an executable script. Two env vars are exported: `ANKH_KUBE_CONTEXT` is the `kube-context` from the current `Context`. `ANKH_CONFIG_GLOBAL` is the `Global` config section from the current `Context` provided as yaml|

#### `Chart`
| Field         |Type| Description   |
| ------------- |:---:|:-------------:|
| name          |string|The chart name. May be the name of a chart in a Helm registry, or the name of a subdirectory (with a valid Chart layout - see Helm documentation on this) under `charts` from the directory where Ankh is run.|
| version       |string|Optional. The chart version, if pulling from a Helm registry.|
| default-values|RawYaml|Optional. Values to use for all environment classes and resource profiles.|
| values        |map[string]RawYaml|Optional. Values to use, by environment class.|
| resource-profiles|map[string]RawYaml|Optional. Values to use, by resource profile.|
