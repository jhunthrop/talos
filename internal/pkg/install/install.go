/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

package install

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/autonomy/talos/internal/pkg/constants"
	"github.com/autonomy/talos/internal/pkg/userdata"
	"github.com/pkg/errors"
)

// Install fetches the necessary data locations and copies or extracts
// to the target locations
// nolint: gocyclo
func Install(data *userdata.UserData) (err error) {
	if data.Install == nil {
		return nil
	}

	dataURLs := make(map[string][]string)
	dataURLs[path.Join(constants.NewRoot, constants.RootMountPoint)] = data.Install.Root.Data

	if data.Install.Boot != nil {
		dataURLs[path.Join(constants.NewRoot, constants.BootMountPoint)] = data.Install.Boot.Data
	}

	var sourceFile *os.File
	var destFile *os.File

	var previousMountPoint string
	for dest, urls := range dataURLs {

		if dest != previousMountPoint {
			log.Printf("Downloading assets for %s\n", dest)
			previousMountPoint = dest
		}

		// Extract artifact if necessary, otherwise place at root of partition/filesystem
		for _, artifact := range urls {
			log.Println(artifact)
			switch {
			case strings.HasPrefix(artifact, "http"):
				var u *url.URL
				log.Printf("Downloading %s\n", artifact)
				u, err = url.Parse(artifact)
				if err != nil {
					return err
				}

				sourceFile, err = download(u, dest)
				if err != nil {
					return err
				}

				// TODO add support for checksum validation of downloaded file
			case strings.HasPrefix(artifact, "/"):
				sourceFile, err = os.Open(artifact)
				if err != nil {
					return err
				}
				destFile, err = os.Create(filepath.Join(dest, filepath.Base(artifact)))
				if err != nil {
					return err
				}
			default:
				// Local directories/links
				link := strings.Split(artifact, ":")
				if len(link) == 1 {
					if err = os.MkdirAll(filepath.Join(dest, artifact), 0755); err != nil {
						return err
					}
				} else {
					if err = os.Symlink(link[1], filepath.Join(dest, link[0])); err != nil && !os.IsExist(err) {
						return err
					}
				}
			}

			switch {
			case strings.HasSuffix(sourceFile.Name(), ".tar") || strings.HasSuffix(sourceFile.Name(), ".tar.gz"):
				log.Printf("Extracting %s\n", artifact)
				err = untar(sourceFile, dest)
				if err != nil {
					return err
				}
			case strings.HasPrefix(sourceFile.Name(), "/"):
				if _, err = io.Copy(destFile, sourceFile); err != nil {
					return err
				}

				if err = destFile.Close(); err != nil {
					return err
				}
			default:
			}

			if err = sourceFile.Close(); err != nil {
				return err
			}
			if err = os.Remove(sourceFile.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Simple extract function
// nolint: gocyclo, dupl
func untar(tarball *os.File, dst string) error {

	var input io.Reader
	var err error

	if strings.HasSuffix(tarball.Name(), ".tar.gz") {
		input, err = gzip.NewReader(tarball)
		if err != nil {
			return err
		}

		// nolint: errcheck
		defer input.(*gzip.Reader).Close()
	} else {
		input = tarball
	}

	tr := tar.NewReader(input)

	for {
		var header *tar.Header

		header, err = tr.Next()

		switch {
		case err == io.EOF:
			err = nil
			return err
		case err != nil:
			return err
		case header == nil:
			continue
		}

		// the target location where the dir/file should be created
		target := filepath.Join(dst, header.Name)

		// May need to add in support for these
		/*
			// Type '1' to '6' are header-only flags and may not have a data body.
				TypeLink    = '1' // Hard link
				TypeSymlink = '2' // Symbolic link
				TypeChar    = '3' // Character device node
				TypeBlock   = '4' // Block device node
				TypeDir     = '5' // Directory
				TypeFifo    = '6' // FIFO node
		*/
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			var downloadedFileput *os.File

			downloadedFileput, err = os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			if _, err = io.Copy(downloadedFileput, tr); err != nil {
				return err
			}

			err = downloadedFileput.Close()
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			dest := filepath.Join(dst, header.Name)
			source := header.Linkname
			if err := os.Symlink(source, dest); err != nil {
				return err
			}
		}
	}
}

func download(artifact *url.URL, base string) (*os.File, error) {
	downloadedFile, err := os.Create(filepath.Join(base, filepath.Base(artifact.Path)))
	if err != nil {
		return nil, err
	}

	// Get the data
	resp, err := http.Get(artifact.String())
	if err != nil {
		return downloadedFile, err
	}

	// nolint: errcheck
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		// nolint: errcheck
		downloadedFile.Close()
		return nil, errors.Errorf("Failed to download %s, got %d", artifact, resp.StatusCode)
	}

	// Write the body to file
	_, err = io.Copy(downloadedFile, resp.Body)
	if err != nil {
		return downloadedFile, err
	}

	// Reset downloadedFile file position to 0 so we can immediately read from it
	_, err = downloadedFile.Seek(0, 0)

	return downloadedFile, err
}
