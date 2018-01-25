# ankh

Another Kubernetes Helper

## Installation

```sh
pip install virtualenv
virtualenv venv
./setup.py install
```

## Setup

```sh
source activate # this will activate `venv` and also add $PWD/scripts to your PATH
```

## Usage

```sh
ankh deploy -f $CONFIG

config/minikube-production-local.yaml serves as a simple example config for deploying to "minikube"
```
