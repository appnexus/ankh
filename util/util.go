package util

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v2"

	ankh "github.com/appnexus/ankh/context"
	"github.com/coreos/go-semver/semver"
	"github.com/manifoldco/promptui"
	"github.com/sirupsen/logrus"
	"github.com/technosophos/moniker"
)

type CustomFormatter struct {
	IsTerminal bool
}

func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	red := "\x1B[31m"
	green := "\x1B[32m"
	yellow := "\x1B[33m"
	cyan := "\x1B[36m"
	reset := "\x1B[0m"

	if !f.IsTerminal {
		red = ""
		green = ""
		yellow = ""
		cyan = ""
		reset = ""
	}

	var prefix string
	var color string

	switch entry.Level {
	case logrus.DebugLevel:
		prefix = "DEBUG"
		color = cyan
	case logrus.InfoLevel:
		prefix = "INFO"
		color = green
	case logrus.WarnLevel:
		prefix = "WARNING"
		color = yellow
	case logrus.ErrorLevel:
		prefix = "ERROR"
		color = red
	case logrus.FatalLevel:
		prefix = "FATAL"
		color = red
	case logrus.PanicLevel:
		prefix = "PANIC"
		color = red
	}

	return []byte(fmt.Sprintf("# %s%-8s%s%s\n", color, prefix, reset, entry.Message)), nil
}

// Untar takes a destination path and a reader; a tar reader loops over the tarfile
// creating the file structure at 'dst' along the way, and writing any files
func Untar(dst string, r io.Reader) error {

	gzr, err := gzip.NewReader(r)
	defer gzr.Close()
	if err != nil {
		return err
	}

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()

		switch {

		// if no more files are found return
		case err == io.EOF:
			return nil

		// return any other error
		case err != nil:
			return err

		// if the header is nil, just skip it (not sure how this happens)
		case header == nil:
			continue
		}

		// the target location where the dir/file should be created
		target := filepath.Join(dst, header.Name)

		// the following switch could also be done using fi.Mode(), not sure if there
		// a benefit of using one vs. the other.
		// fi := header.FileInfo()

		// check the file type
		switch header.Typeflag {

		// TODO: find out why header.Typeflag is a uint8 and tar.TypeDir is an
		// int32? For some reason the tarballs coming out of helm don't have
		// directories as separate entries, so all the directories get created by
		// the `case 0` code
		case tar.TypeDir:
			if _, err := os.Stat(target); err != nil {
				if err := os.MkdirAll(target, 0755); err != nil {
					return err
				}
			}

		// if it's a file create it
		case tar.TypeRegA:
			fallthrough
		case tar.TypeReg:
			dir := filepath.Dir(target)
			// sometimes we have to mkdir -p the directories to contain the files we extract
			if _, err := os.Stat(dir); err != nil {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return err
				}
			}

			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			defer f.Close()

			// copy over contents
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
		}
	}
}

/* MIT License
 *
 * Copyright (c) 2017 Roland Singer [roland.singer@desertbit.com]
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

// CopyFile copies the contents of the file named src to the file named
// by dst. The file will be created if it does not already exist. If the
// destination file exists, all it's contents will be replaced by the contents
// of the source file. The file mode will be copied from the source and
// the copied data is synced/flushed to stable storage.
func CopyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer func() {
		if e := out.Close(); e != nil {
			err = e
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		return
	}

	err = out.Sync()
	if err != nil {
		return
	}

	si, err := os.Stat(src)
	if err != nil {
		return
	}
	err = os.Chmod(dst, si.Mode())
	if err != nil {
		return
	}

	return
}

// CopyDir recursively copies a directory tree, attempting to preserve permissions.
// Source directory must exist, destination directory must *not* exist.
// Symlinks are ignored and skipped.
func CopyDir(src string, dst string) (err error) {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)

	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !si.IsDir() {
		return fmt.Errorf("source is not a directory")
	}

	_, err = os.Stat(dst)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	if err == nil {
		return fmt.Errorf("destination already exists")
	}

	err = os.MkdirAll(dst, si.Mode())
	if err != nil {
		return
	}

	entries, err := ioutil.ReadDir(src)
	if err != nil {
		return
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			err = CopyDir(srcPath, dstPath)
			if err != nil {
				return
			}
		} else {
			// Skip symlinks.
			if entry.Mode()&os.ModeSymlink != 0 {
				continue
			}

			err = CopyFile(srcPath, dstPath)
			if err != nil {
				return
			}
		}
	}

	return
}

func Contains(slice []string, search string) bool {
	for _, item := range slice {
		if item == search {
			return true
		}
	}
	return false
}

// MultiErrorFormat takes a slice of errors and returns them as a combined
// string
func MultiErrorFormat(errs []error) string {
	s := []string{}

	for _, e := range errs {
		s = append(s, e.Error())
	}

	return strings.Join(s, "\n")
}

// LineDiff takes two strings and returns a description of the first differing line.
func LineDiff(expected, found string) string {
	if expected == found {
		return ""
	}

	a := strings.SplitAfter(expected, "\n")
	b := strings.SplitAfter(found, "\n")
	out := ""
	expected = ""
	found = ""
	i := 0

	for ; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			break
		}
	}

	if i < len(a) {
		expected = a[i]
	}

	if i < len(b) {
		found = b[i]
	}

	out += fmt.Sprintf("Diff at line %d", i+1)
	out += fmt.Sprintf("\nExpected: '%s'", strconv.Quote(expected))
	out += fmt.Sprintf(", found: '%s'", strconv.Quote(found))
	return out
}

func MapSliceRegexMatch(mapSlice yaml.MapSlice, key string) (interface{}, error) {
	for _, item := range mapSlice {
		regex, ok := item.Key.(string)
		if !ok {
			return nil, fmt.Errorf("Could not parse key as string: %v", item.Key)
		}
		// We definitely do not want to match substrings, so we anchor the provided
		// regex with ^ at the front and $ at the back.
		wholeRegex := fmt.Sprintf("^%v$", regex)
		matched, err := regexp.MatchString(wholeRegex, key)
		if err != nil {
			return nil, fmt.Errorf("Failed to evaluate regex %v over key %v: %v", key, wholeRegex, err)
		}

		if !matched {
			continue
		}

		return item.Value, nil
	}
	return nil, nil
}

func CreateReducedYAMLFile(filename, key string, required bool) ([]byte, error) {
	in := yaml.MapSlice{}
	var result []byte
	inBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return result, err
	}

	if err = yaml.Unmarshal(inBytes, &in); err != nil {
		return result, err
	}

	out, err := MapSliceRegexMatch(in, key)
	if err != nil {
		return result, err
	}
	if out == nil {
		if required {
			return result, fmt.Errorf("missing `%s` key", key)
		} else {
			return result, nil
		}
	}

	outBytes, err := yaml.Marshal(&out)
	if err != nil {
		return result, err
	}

	if err := ioutil.WriteFile(filename, outBytes, 0644); err != nil {
		return result, err
	}

	return outBytes, nil
}

func ArrayDedup(a []string) []string {
	keys := []string{}
	valueMap := make(map[string]struct{})
	for _, s := range a {
		valueMap[s] = struct{}{}
	}
	for k, _ := range valueMap {
		keys = append(keys, k)
	}
	return keys
}

func compareTokens(t1, t2 string) int {
	// split on most things are are not a numeric. this will
	// allow us to mostly compare by parsed numbers, and
	// still fall back to string comparison when this isn't possible.
	seps := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ~!@#$%^&*()_+-={}[]<>?/"
	t1parts := strings.FieldsFunc(t1, func(r rune) bool {
		return strings.Contains(seps, string(r))
	})
	t2parts := strings.FieldsFunc(t2, func(r rune) bool {
		return strings.Contains(seps, string(r))
	})
	for i := 0; i < len(t1parts); i++ {
		if i > len(t2parts)-1 {
			// t2 has fewer parts than t1, so t1 is not less than
			return 1
		}
		x, e1 := strconv.ParseInt(t1parts[i], 10, 0)
		y, e2 := strconv.ParseInt(t2parts[i], 10, 0)
		if e1 != nil || e2 != nil {
			// fall back to string compare if there's
			// any unexpected character
			return strings.Compare(t1, t2)
		} else if x < y {
			return -1
		} else if x > y {
			return 1
		}
	}

	return len(t1parts) - len(t2parts)
}

// Loosely fits the best-effot semver sort implemented by `sort -V`
// Force all tag names that do not begin with a number to be less
// than anything that does start with a number. This prevents
// tags that aren't even close to semver from sorting to the top.
func FuzzySemVerCompare(s1, s2 string) bool {
	s1Number := len(s1) > 0 && unicode.IsNumber(rune(s1[0]))
	s2Number := len(s2) > 0 && unicode.IsNumber(rune(s2[0]))
	if !s1Number && !s2Number {
		// just string compare anything that does not start with a number
		return strings.Compare(s1, s2) < 0
	} else if s1Number && !s2Number {
		// s1 starts with a number, s2 does not. so, s1 is not less than.
		return false
	} else if !s1Number && s2Number {
		// s1 does not start with a number, and s2 does, so s1 is lesser
		return true
	}

	s1parts := strings.Split(s1, ".")
	s2parts := strings.Split(s2, ".")

	for i := 0; i < len(s1parts); i++ {
		if i > len(s2parts)-1 {
			// s2 has fewer parts than s1, so s1 is not less than
			return false
		}
		c := compareTokens(s1parts[i], s2parts[i])
		if c != 0 {
			return c <= 0
		}
	}

	return len(s1parts) <= len(s2parts)
}

func PromptForUsernameWithLabel(label string) (string, error) {
	current_user, err := user.Current()
	if err != nil {
		return "", err
	}

	user_prompt := promptui.Prompt{
		Label:   label,
		Default: current_user.Username,
	}
	username, err := user_prompt.Run()
	if err != nil {
		return "", err
	}
	return strings.Trim(username, " "), nil
}

func PromptForPasswordWithLabel(label string) (string, error) {
	passwordPrompt := promptui.Prompt{
		Label: label,
		Mask:  '*',
	}
	password, err := passwordPrompt.Run()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(password), nil
}

func PromptForInput(defaultValue string, label string) (string, error) {
	prompt := promptui.Prompt{
		Label:   label,
		Default: defaultValue,
	}

	input, err := prompt.Run()
	if err != nil {
		return "", err
	}
	return input, nil
}

func promptForSelectionFzf(choices []string, label string, firstRowHeader bool) (string, error) {
	fzfArgs := []string{}
	headerExtra := ""
	if firstRowHeader && len(choices) > 1 {
		headerExtra = fmt.Sprintf("\n%v", choices[0])
		choices = choices[1:]
	}
	fzfArgs = append(fzfArgs, []string{"--header", fmt.Sprintf("%s (use the arrow keys to browse, or type to fuzzy search)%v", label, headerExtra)}...)
	fzfArgs = append(fzfArgs, []string{"--layout", "reverse", "--height", "20%", "--min-height", "10"}...)
	fzf := exec.Command("fzf", fzfArgs...)
	inPipe, _ := fzf.StdinPipe()
	outPipe, _ := fzf.StdoutPipe()
	fzf.Stderr = os.Stderr

	err := fzf.Start()
	if err != nil {
		return "", err
	}

	input := strings.Join(choices, "\n")
	inPipe.Write([]byte(input))
	inPipe.Close()

	buf, err := ioutil.ReadAll(outPipe)
	if err != nil {
		panic(err)
	}

	err = fzf.Wait()
	if err != nil {
		return "", err
	}

	out := strings.Trim(string(buf), "\n")
	return out, nil
}

func promptForSelection(choices []string, label string, firstRowHeader bool) (string, error) {
	if firstRowHeader && len(choices) > 1 {
		label = fmt.Sprintf("%v\n%v", label, choices[0])
		choices = choices[1:]
	}
	prompt := promptui.Select{
		Label: label,
		Items: choices,
		Size:  10,
	}

	_, choice, err := prompt.Run()
	if err != nil {
		return "", err
	}
	return choice, nil
}

func hasFzf() bool {
	cmd := exec.Command("fzf", "-h")
	if err := cmd.Run(); err != nil {
		return false
	}

	return true
}

func PromptForSelection(choices []string, label string, firstRowHeader bool) (string, error) {
	if hasFzf() {
		return promptForSelectionFzf(choices, label, firstRowHeader)
	} else {
		return promptForSelection(choices, label, firstRowHeader)
	}
}

func PromptForSelectionWithAdd(choices []string, label string, addLabel string) (string, error) {
	prompt := promptui.SelectWithAdd{
		Label:    label,
		Items:    choices,
		AddLabel: addLabel,
	}

	_, choice, err := prompt.Run()
	if err != nil {
		return "", err
	}
	return choice, nil
}

func SemverBump(version string, semVerType string) (string, error) {
	v, err := semver.NewVersion(version)
	if err != nil {
		return "", err
	}

	switch strings.ToLower(semVerType) {
	case "major":
		v.BumpMajor()
	case "minor":
		v.BumpMinor()
	case "patch":
		v.BumpPatch()
	default:
		return "", fmt.Errorf("Unsupported semantic version type '%v'. Must be one of 'major', 'minor', or 'patch'", semVerType)
	}

	return v.String(), nil
}

// GetEnvironmentOrContext, given a enviroment and a context returns the non-empty value
// NOTE: context and enviroment should not both be provided
func GetEnvironmentOrContext(environment string, context string) string {
	if environment != "" {
		return environment
	}
	if context != "" {
		return context
	}
	return ""
}

func NotificationString(notificationFormat string, chart *ankh.Chart, envOrContext string) (string, error) {

	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	chartName := chart.Name
	chartVersion := ""
	chartString := ""
	if chart.Path != "" {
		absChartPath, err := filepath.Abs(chart.Path)
		if err != nil {
			return "", nil
		}
		chartVersion = fmt.Sprintf("%s (local)", absChartPath)
		chartString = chartVersion
	} else {
		chartVersion = chart.Version
		chartString = fmt.Sprintf("%s@%s", chartName, chartVersion)
	}

	version := ""
	if chart.Tag != nil {
		version = *chart.Tag
	}

	result := notificationFormat
	result = strings.Replace(result, "%USER%", currentUser.Username, -1)
	result = strings.Replace(result, "%CHART_NAME%", chartName, -1)
	result = strings.Replace(result, "%CHART_VERSION%", chartVersion, -1)
	result = strings.Replace(result, "%CHART%", chartString, -1)
	result = strings.Replace(result, "%VERSION%", version, -1)
	result = strings.Replace(result, "%TARGET%", envOrContext, -1)

	return result, nil
}

// GenerateName generates a name based on the current working directory or a random name.
func GenerateName(ctx *ankh.ExecutionContext, appName string) string {
	if appName != "" {
		return appName
	}
	cwd, err := os.Getwd()
	if err == nil {
		return filepath.Base(cwd)
	}

	namer := moniker.New()
	return namer.NameSep("-")
}

// UpdateFile replaces a string in a file with a new one
func UpdateFile(filename string, newString string, oldString string) error {
	input, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}

	output := bytes.Replace(input, []byte(oldString), []byte(newString), -1)

	if err = ioutil.WriteFile(filename, output, 0666); err != nil {
		return err
	}
	return nil
}

// FilterStringsContaining filters out strings from stringSlice that contain the substring.
func FilterStringsContaining(stringSlice []string, substring string) []string {
	if len(substring) > 0 {
		return FilterStrings(stringSlice, func(s string) bool { return !strings.Contains(s, substring) })
	}
	return stringSlice
}

// FilterStrings out based on matcher implementation.
func FilterStrings(strings []string, matcher func(string) bool) (ret []string) {
	for _, s := range strings {
		if matcher(s) {
			ret = append(ret, s)
		}
	}
	return
}
