package config

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/kubectl"
)

type ConfigMap struct {
	Data map[string]interface{} `yaml:"data"`
}

func GetAnkhConfig(ctx *ankh.ExecutionContext, configPath string) (ankh.AnkhConfig, error) {
	ankhConfig := ankh.AnkhConfig{}

	u, err := url.Parse(configPath);
	if err != nil {
		return ankhConfig, fmt.Errorf("Could not parse configPath '%v' as a URL: %v", configPath, err)
	}

	body := []byte{}
	if u.Scheme == "http" || u.Scheme == "https" {
		resp, err := http.Get(configPath)
		if err != nil {
			return ankhConfig, fmt.Errorf("Unable to fetch ankh config from URL '%s': %v", configPath, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ankhConfig, fmt.Errorf("Non-200 status code when fetching ankh config from URL '%s': %v", configPath, resp.Status)
		}
		body, err = ioutil.ReadAll(resp.Body)
	} else if u.Scheme == "kubecontext" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 3 {
			return ankhConfig, fmt.Errorf("Malformed URL: kubecontext:// schemes require a path that contains `/kubecontext/namespace/name/key`. Found: \"%s\"", u.Path)
		}

		// Big hack. We should not be inventing schemes like this, but it seems easiest for now.
		ns, name, key := parts[0], parts[1], parts[2]

		kubectlOutput, err := kubectl.GetYaml(ctx, "configmap", u.Host, ns, name)
		if err != nil {
			return ankhConfig, fmt.Errorf("Unable to fetch ankh config from kubecontext '%s': %v", configPath, err)
		}

		// This code is terrible. Eventually, use the real k8s client API and rip out hand-rolled code.
		body = []byte(kubectlOutput)
		configMap := ConfigMap{}
		err = yaml.Unmarshal(body, &configMap)
		if err != nil {
			return ankhConfig, fmt.Errorf("Failed to unmarshal ConfigMap yaml from kubecontext: '%s': %v", configPath, err)
		}

		ctx.Logger.Debugf("Got ConfigMap: %+v", configMap)
		data, ok := configMap.Data[key]
		if !ok {
			return ankhConfig, fmt.Errorf("Could not read key %v in ConfigMap yaml from kubecontext: '%s'", key, configPath)
		}
		str, ok := data.(string)
		if !ok {
			return ankhConfig, fmt.Errorf("Could not convert value %v to string for key %v in ConfigMap yaml from kubecontext: '%s'",
				data, key, configPath)
		}
		body = []byte(str)
	} else {
		body, err = ioutil.ReadFile(configPath)
	}
	if err != nil {
		return ankhConfig, fmt.Errorf("Unable to read ankh config '%s': %v", configPath, err)
	}

	if err := os.MkdirAll(ctx.DataDir, 0755); err != nil {
		return ankhConfig, fmt.Errorf("Unable to make data dir '%s': %v", ctx.DataDir, err)
	}

	err = yaml.UnmarshalStrict(body, &ankhConfig)
	if err != nil {
		return ankhConfig, fmt.Errorf("Error loading ankh config '%s': %v", configPath, err)
	}

	return ankhConfig, nil
}
