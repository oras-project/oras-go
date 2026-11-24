/*
Copyright The ORAS Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package file

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
)

// tarDirectory walks the directory specified by path, and tar those files with a new
// path prefix.
func tarDirectory(ctx context.Context, root, prefix string, w io.Writer, removeTimes bool, buf []byte) (err error) {
	tw := tar.NewWriter(w)
	defer func() {
		closeErr := tw.Close()
		if err == nil {
			err = closeErr
		}
	}()

	return filepath.Walk(root, func(path string, info os.FileInfo, err error) (returnErr error) {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Rename path
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name = filepath.Join(prefix, name)
		name = filepath.ToSlash(name)

		// Generate header
		// NOTE: We don't support hard links and treat it as regular files
		var link string
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		header.Name = name
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""

		if removeTimes {
			header.ModTime = time.Time{}
			header.AccessTime = time.Time{}
			header.ChangeTime = time.Time{}
		}

		// Write file
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if mode.IsRegular() {
			fp, err := os.Open(path)
			if err != nil {
				return err
			}
			defer func() {
				closeErr := fp.Close()
				if returnErr == nil {
					returnErr = closeErr
				}
			}()

			if _, err := io.CopyBuffer(tw, fp, buf); err != nil {
				return fmt.Errorf("failed to copy to %s: %w", path, err)
			}
		}

		return nil
	})
}

// extractTarGzipIn decompresses the gzip and extracts the tar file to the
// directory dirRel inside root. dirPath is the absolute path of that
// directory. Every file system operation goes through root, so no entry can
// be created, modified or followed outside of it, even when a symlink is
// planted concurrently, for example by another layer.
func extractTarGzipIn(root *os.Root, dirRel, dirPath, dirName, gzPath, checksum string, buf []byte, preservePermissions bool) (err error) {
	fp, err := os.Open(gzPath)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := fp.Close()
		if err == nil {
			err = closeErr
		}
	}()

	gzr, err := gzip.NewReader(fp)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := gzr.Close()
		if err == nil {
			err = closeErr
		}
	}()

	var r io.Reader = gzr
	var verifier digest.Verifier
	if checksum != "" {
		if digest, err := digest.Parse(checksum); err == nil {
			verifier = digest.Verifier()
			r = io.TeeReader(r, verifier)
		}
	}
	if err := extractTarDirectoryIn(root, dirRel, dirPath, dirName, r, buf, preservePermissions); err != nil {
		return err
	}
	if verifier != nil && !verifier.Verified() {
		return errors.New("content digest mismatch")
	}
	return nil
}

// extractTarDirectoryIn extracts the tar stream r to the directory dirRel
// inside root. dirPath is the absolute path of that directory and dirName its
// name in the archive.
//
// The lexical checks on entry names and link targets below are policy: they
// keep each entry, and each symlink as written, inside the extraction
// directory. They cannot guarantee containment on their own, because the
// kernel resolves ".." after a symlink differently from a lexical join, and
// because the tree can change between a check and its use. Containment is
// enforced by root, which resolves every path component by component and
// refuses to leave the directory it was opened on.
func extractTarDirectoryIn(root *os.Root, dirRel, dirPath, dirName string, r io.Reader, buf []byte, preservePermissions bool) error {
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		// Name check
		filename := header.Name
		filePathRel, err := resolveRelToBase(dirPath, dirName, filename)
		if err != nil {
			return err
		}
		// Only a directory entry may name the extraction directory itself.
		// Any other entry would replace it, and the entries that follow would
		// be written wherever the replacement resolves.
		if filepath.Clean(filePathRel) == "." && header.Typeflag != tar.TypeDir {
			return fmt.Errorf("entry %q replaces the extraction directory %q: %w", filename, dirName, ErrPathTraversalDisallowed)
		}
		filePath := filepath.Join(dirPath, filePathRel)
		name := filepath.Join(dirRel, filePathRel) // the path inside root

		// resolveRelToBase only performs lexical and per-component Lstat checks,
		// which a chain of previously-extracted symlinks can bypass. Re-verify
		// containment with symlinks fully resolved before mutating the
		// filesystem, matching the check on the pushFile path.
		// (GHSA-m37j-52j7-pjw7)
		if err := checkSymlinkEscape(dirPath, filePath); err != nil {
			return err
		}

		// Create content
		switch header.Typeflag {
		case tar.TypeReg:
			err = writeFileIn(root, name, tr, header.FileInfo().Mode(), buf)
		case tar.TypeDir:
			err = makeDirIn(root, name, header.FileInfo().Mode())
		case tar.TypeLink:
			// NOTE: ORAS does not generate hard links when creating tarballs.
			// If a hard link is found in the tarball, it will be extracted.
			// If the target link already exists, os.Link will throw an error.
			// This is a known limitation and will not be addressed.
			err = extractHardLink(root, dirRel, dirPath, dirName, name, header.Linkname)
		case tar.TypeSymlink:
			var target string
			target, err = ensureLinkPath(dirPath, dirName, filePath, header.Linkname)
			if err != nil {
				return err
			}
			if err := makeSymlinkIn(root, name, target, dirPath, filePath); err != nil {
				return err
			}
			// os.Chtimes and os.Chmod follow symlinks, so restoring the
			// metadata of a symlink entry would modify its target, which may
			// resolve outside the extraction root (GHSA-jh75-xv39-vhvx).
			// Leave the symlink's own metadata as created.
			continue
		default:
			continue // Non-regular files are skipped
		}
		if err != nil {
			return err
		}

		// Never restore metadata through a symlink (GHSA-jh75-xv39-vhvx).
		if fi, err := root.Lstat(name); err != nil {
			return err
		} else if fi.Mode()&os.ModeSymlink != 0 {
			continue
		}

		// Change access time and modification time if possible (error ignored)
		_ = root.Chtimes(name, header.AccessTime, header.ModTime)

		// Restore full mode bits. os.Chmod applied the permission bits of
		// header.Mode only, and root.Chmod rejects the others.
		if preservePermissions && (header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeDir) {
			if err := root.Chmod(name, os.FileMode(header.Mode).Perm()); err != nil {
				return err
			}
		}
	}
}

// extractHardLink creates a hard link at name, a path inside root, to the file
// named by linkname. Unlike a symlink target, a tar hard link target is a path
// from the archive root, the same convention as entry names, so it is resolved
// against dirPath and not against the directory of the link.
func extractHardLink(root *os.Root, dirRel, dirPath, dirName, name, linkname string) error {
	rel, err := resolveRelToBase(dirPath, dirName, linkname)
	if err != nil {
		return err
	}
	if err := checkSymlinkEscape(dirPath, filepath.Join(dirPath, rel)); err != nil {
		return err
	}
	target := filepath.Join(dirRel, rel)
	// link(2) may follow a symlink target on some platforms, and the checks
	// above only inspect the ancestors of target, so refuse a symlink target.
	info, err := root.Lstat(target)
	if err != nil {
		return fmt.Errorf("hard link %q: %w", linkname, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("hard link target %q is a symbolic link", linkname)
	}
	return root.Link(target, name)
}

// resolveRelToBase ensures the target path is in the base path,
// returning its relative path to the base path.
// target can be either an absolute path or a relative path.
func resolveRelToBase(baseAbs, baseRel, target string) (string, error) {
	base := baseRel
	if filepath.IsAbs(target) {
		// ensure base and target are consistent
		base = baseAbs
	}
	path, err := filepath.Rel(base, target)
	if err != nil {
		return "", err
	}
	cleanPath := filepath.ToSlash(filepath.Clean(path))
	if cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		return "", fmt.Errorf("%q is outside of %q", target, baseRel)
	}

	// No symbolic link allowed in the relative path
	dir := filepath.Dir(path)
	for dir != "." {
		if info, err := os.Lstat(filepath.Join(baseAbs, dir)); err != nil {
			if !os.IsNotExist(err) {
				return "", err
			}
		} else if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("no symbolic link allowed between %q and %q", baseRel, target)
		}
		dir = filepath.Dir(dir)
	}

	return path, nil
}

// ensureLinkPath ensures the target path pointed by the link is in the base
// path. It returns target path if validated.
func ensureLinkPath(baseAbs, baseRel, link, target string) (string, error) {
	// resolve link
	path := target
	if !filepath.IsAbs(target) {
		path = filepath.Join(filepath.Dir(link), target)
	}
	// ensure path is under baseAbs or baseRel
	if _, err := resolveRelToBase(baseAbs, baseRel, path); err != nil {
		return "", err
	}
	return target, nil
}

// makeDirIn creates the directory name inside root.
func makeDirIn(root *os.Root, name string, perm os.FileMode) error {
	// MkdirAll follows a terminal symlink and returns nil if it resolves to
	// an existing directory, so a directory entry whose path was already
	// created as a symlink by an earlier archive entry would silently reuse
	// that link, and the metadata restored afterwards would be applied to the
	// directory it points to (GHSA-jh75-xv39-vhvx). Remove any such symlink
	// first so a real directory is created at name, as writeFileIn does for
	// regular files.
	if err := removeSymlinkIn(root, name); err != nil {
		return err
	}
	// root.MkdirAll accepts permission bits only, not the os.ModeDir of a tar
	// header.
	return root.MkdirAll(name, perm.Perm())
}

// makeSymlinkIn creates the symlink name inside root, pointing to target. An
// existing file or symlink at name is replaced, as tar does. An existing
// directory is not: replacing a directory with a link would redirect whatever
// is extracted into it later, for example the entries of another layer
// unpacked below it.
//
// link is the absolute path of name, and the symlink must resolve inside
// dirPath, the directory the lexical check on target bounds it to.
func makeSymlinkIn(root *os.Root, name, target, dirPath, link string) error {
	err := root.Symlink(target, name)
	if errors.Is(err, fs.ErrExist) {
		fi, statErr := root.Lstat(name)
		if statErr != nil {
			return statErr
		}
		if fi.IsDir() {
			return fmt.Errorf("symlink %q would replace a directory: %w", name, ErrPathTraversalDisallowed)
		}
		// link already exists, remove the old one and try again
		if err := root.Remove(name); err != nil {
			return err
		}
		err = root.Symlink(target, name)
	}
	if err != nil {
		return err
	}
	// The target was checked lexically, but the kernel resolves ".." after a
	// symlink from where that symlink points, so a target that is inside the
	// extraction directory as text can resolve outside of it. root refuses to
	// follow such a link, so writes through it already fail; remove it as
	// well, so that it is not left for whoever reads the extracted files.
	if err := checkLinkEscape(dirPath, link); err != nil {
		_ = root.Remove(name)
		return fmt.Errorf("symlink %q to %q: %w", name, target, err)
	}
	return nil
}

// checkLinkEscape returns ErrPathTraversalDisallowed if the symlink link,
// resolved the way the kernel resolves it, points outside base. A dangling
// link is accepted: it is checked when a write goes through it.
func checkLinkEscape(base, link string) error {
	realLink, err := filepath.EvalSymlinks(link)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(realBase, realLink)
	if err != nil {
		return ErrPathTraversalDisallowed
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return ErrPathTraversalDisallowed
	}
	return nil
}

// removeSymlinkIn removes name inside root if it is a symlink, so that a
// subsequent write to name does not follow it. It is a no-op if name does not
// exist or is not a symlink.
func removeSymlinkIn(root *os.Root, name string) error {
	fi, err := root.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("failed to remove symlink %s: %w", name, err)
	}
	return nil
}

// writeFileIn writes content to the file name inside root.
func writeFileIn(root *os.Root, name string, r io.Reader, perm os.FileMode, buf []byte) (err error) {
	// Opening a file follows a terminal symlink, so a regular-file entry whose
	// path was already created as a symlink by an earlier archive entry would
	// be written through that link (GHSA-m37j-52j7-pjw7). Remove any such
	// symlink first so the content is written to a regular file at name
	// itself. A link planted after this check is followed, but only inside
	// root.
	if err := removeSymlinkIn(root, name); err != nil {
		return err
	}
	// root.OpenFile accepts permission bits only.
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm.Perm())
	if err != nil {
		return err
	}
	defer func() {
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}()

	_, err = io.CopyBuffer(file, r, buf)
	return err
}
