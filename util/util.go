package util

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
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
		// TODO: why doesn't this line up with tar.TypeReg?
		case 0:
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

func CreateReducedYAMLFile(filename, key string) ([]byte, error) {
	in := make(map[string]interface{})
	var result []byte
	inBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return result, err
	}

	if err = yaml.UnmarshalStrict(inBytes, &in); err != nil {
		return result, err
	}

	out := make(map[interface{}]interface{})

	if in[key] == nil {
		return result, fmt.Errorf("missing `%s` key", key)
	}

	switch t := in[key].(type) {
	case map[interface{}]interface{}:
		for k, v := range t {
			// TODO: using `.(string)` here could cause a panic in cases where the
			// key isn't a string, which is pretty uncommon

			// TODO: validate
			out[k.(string)] = v
		}
	default:
		out[key] = in[key]
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

type HelmChart struct {
	Name string
}

func ReadChartDirectory(chartDir string) (*HelmChart, error) {
	chartYamlPath := filepath.Join(chartDir, "Chart.yaml")
	chartYaml, err := ioutil.ReadFile(chartYamlPath)
	if err != nil {
		return nil, err
	}
	helmChart := HelmChart{}
	err = yaml.Unmarshal(chartYaml, &helmChart)
	if err != nil {
		return nil, err
	}
	if helmChart.Name == "" {
		return nil, fmt.Errorf("Did not find any `name` in %v", chartYamlPath)
	}
	return &helmChart, nil
}
