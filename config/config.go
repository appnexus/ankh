package config

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"

	"github.com/appnexus/ankh/context"
)

type ConfigMap struct {
	Data map[string]interface{} `yaml:"data"`
}

func GetAnkhConfig(ctx *ankh.ExecutionContext, configPath string) (ankh.AnkhConfig, error) {
	ankhConfig := ankh.AnkhConfig{}

	u, err := url.Parse(configPath)
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
	} else {
		body, err = ioutil.ReadFile(configPath)
	}
	if err != nil {
		return ankhConfig, fmt.Errorf("Unable to read ankh config '%s', consider using `ankh config init`: %v", configPath, err)
	}

	if err := os.MkdirAll(ctx.DataDir, 0755); err != nil {
		return ankhConfig, fmt.Errorf("Unable to make data dir '%s': %v", ctx.DataDir, err)
	}

	err = yaml.UnmarshalStrict(body, &ankhConfig)
	if err != nil {
		return ankhConfig, fmt.Errorf("Error loading ankh config '%s': %v", configPath, err)
	}

	// Mark each context and environment as sourced from this configPath
	for name, _ := range ankhConfig.Contexts {
		context := ankhConfig.Contexts[name]
		context.Source = configPath
		ankhConfig.Contexts[name] = context
	}
	for name, _ := range ankhConfig.Environments {
		environment := ankhConfig.Environments[name]
		environment.Source = configPath
		ankhConfig.Environments[name] = environment
	}

	return ankhConfig, nil
}
