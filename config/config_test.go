package config

import (
	"io/ioutil"
	"testing"

	"github.com/appnexus/ankh/context"
)

const minimalValidAnkhConfigYAMLPath string = "testdata/testconfig.yaml"

func TestGetAnkhConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		tmpDir, _ := ioutil.TempDir("", "")
		ctx := &ankh.ExecutionContext{
			AnkhConfigPath: minimalValidAnkhConfigYAMLPath,
			DataDir:        tmpDir,
		}

		_, err := GetAnkhConfig(ctx, ctx.AnkhConfigPath)
		if err != nil {
			t.Log(err)
			t.Fail()
		}
	})

	t.Run("missing config path", func(t *testing.T) {
		tmpDir, _ := ioutil.TempDir("", "")
		ctx := &ankh.ExecutionContext{
			AnkhConfigPath: "/does/not/exist",
			DataDir:        tmpDir,
		}

		_, err := GetAnkhConfig(ctx, ctx.AnkhConfigPath)
		if err == nil {
			t.Log("expected to find an error but didnt get one")
			t.Fail()
		}
	})
}
