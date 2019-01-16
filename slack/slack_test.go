package slack

import (
	"testing"
)

func TestGetSlackMessage(t *testing.T) {
	version := "1.33.7"
	chart := "best app ever"
	env := "production"

	expectedResult := "*best app ever* _v.1.33.7_ is being deployed to *production*"
	result := getMessageText(version, chart, env, "")
	if result != expectedResult {
		t.Logf("got %s but was expecting '%s'", result, expectedResult)
		t.Fail()
	}

	version = "rollback"
	expectedResult = "*best app ever* is being ROLLED BACK to the previous version in *production*"
	result = getMessageText(version, chart, env, "")
	if result != expectedResult {
		t.Logf("got %s but was expecting '%s'", result, expectedResult)
		t.Fail()
	}

	messageOverride := "This should be the message"
	expectedResult = "This should be the message"
	result = getMessageText(version, chart, env, messageOverride)
	if result != expectedResult {
		t.Logf("got %s but was expecting '%s'", result, expectedResult)
		t.Fail()
	}

}
