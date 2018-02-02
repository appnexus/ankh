# ankh

Another Kubernetes Helper

## Dependencies
Some of the bootstrap scripts depend on Python 2 and pyyaml being installed
```sh
pip install pyyaml
```

## Installation

```sh
make # build to bin/
make install # install to /usr/local/bin/
mkdir -p ~/.ankh/config && cp config/ankhconfig.yaml ~/.ankh/config # to get a default config
```

## Usage

```sh
ankh -h
ankh apply
ankh template
```
