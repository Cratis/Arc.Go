// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// openOutputRoot anchors creation at the nearest existing directory. Subsequent
// mutations use Root to prevent symlink replacements from escaping that anchor.
func openOutputRoot(path string) (*os.Root, error) {
	if err := safeOutputPath(path); err != nil {
		return nil, err
	}
	existing := path
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		existing = filepath.Dir(existing)
	}
	anchor, err := os.OpenRoot(existing)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(existing, path)
	if err != nil {
		return nil, errors.Join(err, anchor.Close())
	}
	if err := anchor.MkdirAll(relative, 0755); err != nil {
		return nil, errors.Join(err, anchor.Close())
	}
	root, err := anchor.OpenRoot(relative)
	err = errors.Join(err, anchor.Close())
	if err != nil && root != nil {
		err = errors.Join(err, root.Close())
		root = nil
	}
	return root, err
}

func writeRootOutput(root *os.Root, path string, content []byte) (err error) {
	temporary := filepath.Join(filepath.Dir(path), ".arc-gen-"+rand.Text())
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer func() {
		removeErr := root.Remove(temporary)
		if !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if _, err = file.Write(content); err != nil {
		return errors.Join(err, file.Close())
	}
	if err = file.Close(); err != nil {
		return err
	}
	return root.Rename(temporary, path)
}
