package ankh

import (
	"io/ioutil"

	"github.com/appnexus/ankh/util"
	"github.com/imdario/mergo"
	"gopkg.in/yaml.v2"
)

type ChartParameterSource struct {
	Values []string
	Url    string
}

type ChartParameters struct {
	Key          string               `yaml:"key"`
	SafeDefault  *string              `yaml:"safeDefault"`
	Source       ChartParameterSource `yaml:"source"`
	CachedValue  string               `yaml:"-"`
}

func CreateReducedParametersFile(ctx *ExecutionContext, filename string, parameters []ChartParameters) ([]byte, error) {
	//in := yaml.MapSlice{}
	in := make(map[string]yaml.MapSlice)
	for _, param := range parameters {
		in[param.Key] = yaml.MapSlice{}
		ctx.Logger.Debugf("Initializing parameters input map for key %v\n", param.Key)
	}

	var result []byte
	inBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return result, err
	}

	if err = yaml.Unmarshal(inBytes, &in); err != nil {
		return result, err
	}

	// For every parameter section in the map slice, see if that parameter is set.
	// If it is, try to find a match within the map slice section, and use those values.
	out := make(map[interface{}]interface{})
	for _, param := range parameters {
		ctx.Logger.Debugf("Working on parameter key %v", param.Key)
		section, ok := in[param.Key]
		if !ok || len(section) == 0 {
			ctx.Logger.Debugf("Could not find non-empty parameter section for key %v", param.Key)
			continue
		}

		ctx.Logger.Debugf("Trying to find a regex match for cached value %v inside section %+v", param.CachedValue, section)
		match, err := util.MapSliceRegexMatch(section, param.CachedValue)
		if err != nil {
			return result, err
		}

		ctx.Logger.Debugf("Found match %+v", match)
		for _, o := range match.(yaml.MapSlice) {
			out[o.Key] = o.Value
		}
		mergo.Merge(&out, match)
		ctx.Logger.Debugf("Post-merge out is now %+v", out)
	}

	outBytes, err := yaml.Marshal(&out)
	if err != nil {
		return result, err
	}

	if err := ioutil.WriteFile(filename, outBytes, 0644); err != nil {
		return result, err
	}

	ctx.Logger.Debugf("Wrote %d bytes to %s, body is %v", len(outBytes), filename, string(outBytes))
	return outBytes, nil
}
