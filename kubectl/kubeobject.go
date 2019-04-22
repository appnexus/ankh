package kubectl

import (
	"io"
	"strings"

	"gopkg.in/yaml.v2"
)

type KubeObject struct {
	Kind     string
	Metadata struct {
		Name   string
		Labels map[string]string
	}
	Spec struct {
		Selector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		}
	}
}

func forEachKubeObject(input string, fn func(obj *KubeObject) bool) {
	decoder := yaml.NewDecoder(strings.NewReader(input))

	for {
		obj := KubeObject{}
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}

		if obj.Kind == "" {
			continue
		}

		ok := fn(&obj)
		if !ok {
			return
		}
	}
}
