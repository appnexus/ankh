package util

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
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

	var level string

	switch entry.Level {
	case logrus.DebugLevel:
		level = fmt.Sprintf("%sDEBUG%s", cyan, reset)
	case logrus.InfoLevel:
		level = fmt.Sprintf("%sINFO%s", green, reset)
	case logrus.WarnLevel:
		level = fmt.Sprintf("%sWARNING%s", yellow, reset)
	case logrus.ErrorLevel:
		level = fmt.Sprintf("%sERROR%s", red, reset)
	case logrus.FatalLevel:
		level = fmt.Sprintf("%sFATAL%s", red, reset)
	case logrus.PanicLevel:
		level = fmt.Sprintf("%sPANIC%s", red, reset)
	}

	return []byte(fmt.Sprintf("# %-8s %s\n", level, entry.Message)), nil
}

// Collapse recursively traverses a map and tries to collapse it to a flat
// slice of `key.key.key=value` pairs
func Collapse(x interface{}, path []string, acc []string) []string {
	if path == nil {
		path = []string{}
	}
	if acc == nil {
		acc = []string{}
	}

	switch x := x.(type) {
	case map[string]interface{}:
		var arr []string
		for key, value := range x {
			newPath := append(path, key)
			arr = append(arr, Collapse(value, newPath, acc)...)
		}
		return arr
	// TODO: this could probably be cleaned up, but basically we've got a mix of
	// `interface{}` and `string` keys even though they are really all string
	// keys.
	case map[interface{}]interface{}:
		var arr []string
		for key, value := range x {
			// This will result in a panic if we have something other than a string key.
			newPath := append(path, key.(string))
			arr = append(arr, Collapse(value, newPath, acc)...)
		}
		return arr
	case bool:
		return append(acc, strings.Join(path, ".")+"="+strconv.FormatBool(x))
	case float64:
		return append(acc, strings.Join(path, ".")+"="+strconv.FormatFloat(x, 'f', -1, 64))
	case string:
		return append(acc, strings.Join(path, ".")+"="+string(x))
	case int:
		return append(acc, strings.Join(path, ".")+"="+string(x))
	default:
		// Just exclude datatypes we don't know about. It's possible this isn't
		// handling all the cases that yaml parsing can provide
		return acc
	}
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
