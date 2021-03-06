package util

import (
	"archive/zip"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"

	"go.skia.org/infra/go/sklog"
)

// unzip unzips the file given in src into the 'dest' directory.
func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer Close(r)

	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer Close(rc)

		path := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, f.Mode()); err != nil {
				return err
			}
		} else {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				return err
			}
			defer Close(f)

			_, err = io.Copy(f, rc)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// TempRepo is used to setup and teardown a temporary repo for unit testing.
type TempRepo struct {
	// Root of unzipped Git repo.
	Dir string
}

// NewTempRepoFrom returns a TempRepo instance based on the contents of the
// given zip file path. Unzips to a temporary directory which is stored in
// TempRepo.Dir.
func NewTempRepoFrom(zipfile string) *TempRepo {
	tmpdir, err := ioutil.TempDir("", "skiaperf")
	if err != nil {
		sklog.Fatalln("Failed to create testing Git repo:", err)
	}
	if err := unzip(zipfile, tmpdir); err != nil {
		sklog.Fatalln("Failed to unzip testing Git repo:", err)
	}
	return &TempRepo{Dir: tmpdir}
}

// NewTempRepo assumes the repo is called testrepo.zip and is in a directory
// called testdata under the directory of the unit test that is calling it.
//
// The directory that was created is stored in TempRepo Path.
func NewTempRepo() *TempRepo {
	_, filename, _, _ := runtime.Caller(1)
	return NewTempRepoFrom(filepath.Join(filepath.Dir(filename), "testdata", "testrepo.zip"))
}

// Cleanup cleans up the temporary repo.
func (t *TempRepo) Cleanup() {
	if err := os.RemoveAll(t.Dir); err != nil {
		sklog.Fatalln("Failed to clean up after test:", err)
	}
}
