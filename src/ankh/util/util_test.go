package util

import (
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
		formatterTest{"terminal debug", true, &logrus.Entry{Message: "one", Level: logrus.DebugLevel}, "# \x1B[36mDEBUG\x1B[0m one\n"},
		formatterTest{"terminal info", true, &logrus.Entry{Message: "two", Level: logrus.InfoLevel}, "# \x1B[32mINFO\x1B[0m two\n"},
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

func TestCollapse(t *testing.T) {

}

func TestUntar(t *testing.T) {

}

func TestCopyFile(t *testing.T) {

}

func TestDir(t *testing.T) {

}

func TestContains(t *testing.T) {

}

func TestMultiErrorFormat(t *testing.T) {

}
