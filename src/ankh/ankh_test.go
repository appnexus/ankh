package ankh

import (
	"testing"
)

func TestAnkhConfigValidateAndInit(t *testing.T) {

	t.Run("valid AnkhConfig", func(t *testing.T) {
		ankhConfig := AnkhConfig{
			CurrentContextName: "foo",
			Contexts: map[string]Context{
				"foo": Context{},
			},
		}

		errs := ankhConfig.ValidateAndInit()

		if len(errs) > 0 {
			t.Logf("got errors when trying to validate an AnkhConfig: %v", errs)
			t.Fail()
		}
	})

	t.Run("missing current context", func(t *testing.T) {
		ankhConfig := AnkhConfig{
			CurrentContextName: "foo",
			Contexts: map[string]Context{
				"nope": Context{},
			},
		}

		errs := ankhConfig.ValidateAndInit()

		if len(errs) == 0 {
			t.Logf("was expecting to find an error when missing the current context `errs`: %v", errs)
			t.Fail()
		}
	})

}
