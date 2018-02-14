package util

import (
	"fmt"
	"testing"

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
