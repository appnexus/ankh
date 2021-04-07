package util

import (
	"fmt"
	"os/user"
	"testing"

	ankh "github.com/appnexus/ankh/context"
	"github.com/sirupsen/logrus"
)

func TestCustomFormatterFormat(t *testing.T) {
	type formatterTest struct {
		title      string
		isTerminal bool
		entry      *logrus.Entry
		expected   string
	}

	formatterTests := []formatterTest{
		formatterTest{"terminal debug", true, &logrus.Entry{Message: "one", Level: logrus.DebugLevel}, "# \x1B[36mDEBUG   \x1B[0mone\n"},
		formatterTest{"terminal info", true, &logrus.Entry{Message: "two", Level: logrus.InfoLevel}, "# \x1B[32mINFO    \x1B[0mtwo\n"},
		formatterTest{"terminal warning", true, &logrus.Entry{Message: "three", Level: logrus.WarnLevel}, "# \x1B[33mWARNING \x1B[0mthree\n"},
		formatterTest{"terminal error", true, &logrus.Entry{Message: "four", Level: logrus.ErrorLevel}, "# \x1B[31mERROR   \x1B[0mfour\n"},
		formatterTest{"terminal fatal", true, &logrus.Entry{Message: "five", Level: logrus.FatalLevel}, "# \x1B[31mFATAL   \x1B[0mfive\n"},
		formatterTest{"terminal panic", true, &logrus.Entry{Message: "six", Level: logrus.PanicLevel}, "# \x1B[31mPANIC   \x1B[0msix\n"},
		formatterTest{"debug", false, &logrus.Entry{Message: "one", Level: logrus.DebugLevel}, "# DEBUG   one\n"},
		formatterTest{"info", false, &logrus.Entry{Message: "two", Level: logrus.InfoLevel}, "# INFO    two\n"},
		formatterTest{"warning", false, &logrus.Entry{Message: "three", Level: logrus.WarnLevel}, "# WARNING three\n"},
		formatterTest{"error", false, &logrus.Entry{Message: "four", Level: logrus.ErrorLevel}, "# ERROR   four\n"},
		formatterTest{"fatal", false, &logrus.Entry{Message: "five", Level: logrus.FatalLevel}, "# FATAL   five\n"},
		formatterTest{"panic", false, &logrus.Entry{Message: "six", Level: logrus.PanicLevel}, "# PANIC   six\n"},
	}

	for _, test := range formatterTests {
		t.Run(test.title, func(t *testing.T) {
			formatter := CustomFormatter{
				IsTerminal: test.isTerminal,
			}

			result, err := formatter.Format(test.entry)
			if err != nil {
				t.Log(err)
				t.Fail()
			}

			if string(result) != test.expected {
				t.Logf("expected '%s' but got '%s'", test.expected, result)
				t.Fail()
			}
		})
	}
}

// TODO
func TestUntar(t *testing.T) {}

// TODO
func TestCopyFile(t *testing.T) {}

// TODO
func TestDir(t *testing.T) {}

func TestContains(t *testing.T) {
	t.Run("does contain", func(t *testing.T) {
		result := Contains([]string{"one", "two"}, "two")
		if !result {
			t.Log("got 'false' but was expecting 'true'")
			t.Fail()
		}
	})
	t.Run("does not contain", func(t *testing.T) {
		result := Contains([]string{"one", "three", "four"}, "two")
		if result {
			t.Log("got 'true' but was expecting 'false'")
			t.Fail()
		}
	})
}

func TestMultiErrorFormat(t *testing.T) {
	err1 := fmt.Errorf("one")
	err2 := fmt.Errorf("two")

	expected := "one\ntwo"
	result := MultiErrorFormat([]error{err1, err2})

	if result != expected {
		t.Logf("got '%s' but was expecting '%s'", result, expected)
		t.Fail()
	}
}

func TestGetEnviroment(t *testing.T) {
	env := "staging"
	context := "kube00abc1-dev"

	expectedEnv := "staging"
	expectedContext := "kube00abc1-dev"

	result := GetEnvironmentOrContext(env, "")
	if result != expectedEnv {
		t.Logf("got %s but was expecting '%s'", result, expectedEnv)
		t.Fail()
	}
	result = GetEnvironmentOrContext("", context)
	if result != expectedContext {
		t.Logf("got %s but was expecting '%s'", result, expectedContext)
		t.Fail()
	}
}

func TestNotificationString(t *testing.T) {

	// replace %USER%, %CHART%, %VERSION%, %TARGET% (non-local chart)

	notificationFormat := "%USER% is doing a release of %CHART% version %VERSION% to %TARGET%"
	version := "1.33.7"
	chart := &ankh.Chart{
		Path:    "",
		Name:    "best app ever",
		Version: "1.2.3",
		Tag:     &version,
	}
	envOrContext := "production"

	currentUser, err := user.Current()
	if err != nil {
		t.Logf("Unable to get currnet user for test. Error: %v", err)
		t.Fail()
	}

	expectedResult := fmt.Sprintf("%v is doing a release of best app ever@1.2.3 version 1.33.7 to production", currentUser.Username)
	result, err := NotificationString(notificationFormat, chart, envOrContext)
	if err != nil {
		t.Logf("Failed to replace message text. Error: %v", err)
		t.Fail()
	}
	if result != expectedResult {
		t.Logf("got %s but was expecting '%s'", result, expectedResult)
		t.Fail()
	}

	// -----------------------------------------------------------------

	// replace %CHART%, %VERSION%, %TARGET% (local chart)

	notificationFormat = "Releasing %CHART% version %VERSION% to %TARGET%"
	version = "1.33.7"
	chart = &ankh.Chart{
		Path:    "/home/someone/app/helm/app",
		Name:    "best app ever",
		Version: "1.2.3",
		Tag:     &version,
	}
	envOrContext = "production"

	expectedResult = "Releasing /home/someone/app/helm/app (local) version 1.33.7 to production"
	result, err = NotificationString(notificationFormat, chart, envOrContext)
	if err != nil {
		t.Logf("Failed to replace message text. Error: %v", err)
		t.Fail()
	}
	if result != expectedResult {
		t.Logf("got %s but was expecting '%s'", result, expectedResult)
		t.Fail()
	}

	// -----------------------------------------------------------------

	// replace %CHART_NAME%, %CHART_VERSION%, %VERSION%, %TARGET% (non-local chart)

	notificationFormat = "Releasing %CHART_NAME% chart %CHART_VERSION% version %VERSION% to %TARGET%"
	version = "1.33.7"
	chart = &ankh.Chart{
		Path:    "",
		Name:    "best app ever",
		Version: "1.2.3",
		Tag:     &version,
	}
	envOrContext = "production"

	expectedResult = "Releasing best app ever chart 1.2.3 version 1.33.7 to production"
	result, err = NotificationString(notificationFormat, chart, envOrContext)
	if err != nil {
		t.Logf("Failed to replace message text. Error: %v", err)
		t.Fail()
	}
	if result != expectedResult {
		t.Logf("got %s but was expecting '%s'", result, expectedResult)
		t.Fail()
	}

	// -----------------------------------------------------------------

	// replace %CHART_NAME%, %CHART_VERSION%, %VERSION%, %TARGET% (local chart)

	notificationFormat = "Releasing %CHART_NAME% chart %CHART_VERSION% version %VERSION% to %TARGET%"
	version = "1.33.7"
	chart = &ankh.Chart{
		Path:    "/home/someone/app/helm/app",
		Name:    "best app ever",
		Version: "1.2.3",
		Tag:     &version,
	}
	envOrContext = "production"

	expectedResult = "Releasing best app ever chart /home/someone/app/helm/app (local) version 1.33.7 to production"
	result, err = NotificationString(notificationFormat, chart, envOrContext)
	if err != nil {
		t.Logf("Failed to replace message text. Error: %v", err)
		t.Fail()
	}
	if result != expectedResult {
		t.Logf("got %s but was expecting '%s'", result, expectedResult)
		t.Fail()
	}

	// -----------------------------------------------------------------

	// replace %VERSION%, %TARGET (non-local chart)
	// format also includes (typo?) %CHAT% which is not replaced

	notificationFormat = "Releasing %CHAT% version %VERSION% to %TARGET%"
	version = "1.33.7"
	chart = &ankh.Chart{
		Path:    "",
		Name:    "best app ever",
		Version: "1.2.3",
		Tag:     &version,
	}
	envOrContext = "production"

	expectedResult = "Releasing %CHAT% version 1.33.7 to production"
	result, err = NotificationString(notificationFormat, chart, envOrContext)
	if err != nil {
		t.Logf("Failed to replace message text. Error: %v", err)
		t.Fail()
	}
	if result != expectedResult {
		t.Logf("got %s but was expecting '%s'", result, expectedResult)
		t.Fail()
	}

}
