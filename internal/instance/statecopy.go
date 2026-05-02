package instance

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CopyDirOptions struct {
	Filter func(relPath string, entry fs.DirEntry) (bool, error)
}

func AtomicCopyDir(src, dst string, options CopyDirOptions) error {
	source, destination, err := normalizeDistinctPaths(src, dst)
	if err != nil {
		return err
	}
	if exists, err := pathExists(destination); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("instance: destination %q already exists", destination)
	}

	if err := ensureSourceDir(source); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("instance: create destination parent for %q: %w", destination, err)
	}

	temporary := siblingTempPath(destination, "copy")
	if err := copyDirTree(source, temporary, options); err != nil {
		_ = os.RemoveAll(temporary)
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.RemoveAll(temporary)
		return fmt.Errorf("instance: publish copied directory %q: %w", destination, err)
	}
	return nil
}

func AtomicReplaceDir(src, dst string, options CopyDirOptions) error {
	source, destination, err := normalizeDistinctPaths(src, dst)
	if err != nil {
		return err
	}
	if err := ensureSourceDir(source); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("instance: create destination parent for %q: %w", destination, err)
	}

	newPath := siblingTempPath(destination, "restore-new")
	if err := copyDirTree(source, newPath, options); err != nil {
		_ = os.RemoveAll(newPath)
		return err
	}

	if exists, err := pathExists(destination); err != nil {
		_ = os.RemoveAll(newPath)
		return err
	} else if !exists {
		if err := os.Rename(newPath, destination); err != nil {
			_ = os.RemoveAll(newPath)
			return fmt.Errorf("instance: publish restored directory %q: %w", destination, err)
		}
		return nil
	}

	oldPath := siblingTempPath(destination, "restore-old")
	if err := os.Rename(destination, oldPath); err != nil {
		_ = os.RemoveAll(newPath)
		return fmt.Errorf("instance: move existing directory %q aside: %w", destination, err)
	}
	if err := os.Rename(newPath, destination); err != nil {
		rollbackErr := os.Rename(oldPath, destination)
		_ = os.RemoveAll(newPath)
		if rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("instance: publish restored directory %q: %w", destination, err),
				fmt.Errorf("instance: rollback restored directory %q: %w", destination, rollbackErr),
			)
		}
		return fmt.Errorf("instance: publish restored directory %q: %w", destination, err)
	}
	if err := os.RemoveAll(oldPath); err != nil {
		return fmt.Errorf("instance: remove previous directory %q: %w", oldPath, err)
	}
	return nil
}

func SkipRuntimeArtifacts(relPath string, entry fs.DirEntry) (bool, error) {
	clean := filepath.Clean(relPath)
	if clean == "." {
		return false, nil
	}
	if entry.Type()&os.ModeSocket != 0 {
		return true, nil
	}
	name := filepath.Base(clean)
	if name == socketFileName || name == pidFileName {
		return true, nil
	}
	return false, nil
}

func copyDirTree(src, dst string, options CopyDirOptions) error {
	rootInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("instance: inspect source directory %q: %w", src, err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("instance: source %q is not a directory", src)
	}
	if err := os.Mkdir(dst, rootInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("instance: create directory %q: %w", dst, err)
	}

	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("instance: walk %q: %w", path, walkErr)
		}
		if path == src {
			return nil
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("instance: compute relative path for %q: %w", path, err)
		}
		if options.Filter != nil {
			skip, err := options.Filter(relPath, entry)
			if err != nil {
				return err
			}
			if skip {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		target := filepath.Join(dst, relPath)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("instance: inspect %q: %w", path, err)
		}

		switch {
		case info.IsDir():
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return fmt.Errorf("instance: create directory %q: %w", target, err)
			}
		case info.Mode().IsRegular():
			if err := copyRegularFile(path, target, info.Mode().Perm(), info.ModTime()); err != nil {
				return err
			}
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("instance: refusing to copy symlink %q", path)
		case info.Mode()&os.ModeSocket != 0:
			return fmt.Errorf("instance: refusing to copy socket %q", path)
		default:
			return fmt.Errorf("instance: unsupported file mode %s for %q", info.Mode(), path)
		}
		return nil
	})
}

func copyRegularFile(src, dst string, mode fs.FileMode, modifiedAt time.Time) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("instance: open source file %q: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("instance: create destination file %q: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("instance: copy file %q -> %q: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("instance: close destination file %q: %w", dst, err)
	}
	if err := os.Chtimes(dst, modifiedAt, modifiedAt); err != nil {
		return fmt.Errorf("instance: preserve timestamps for %q: %w", dst, err)
	}
	return nil
}

func normalizeDistinctPaths(src, dst string) (source, destination string, err error) {
	source, err = filepath.Abs(src)
	if err != nil {
		return "", "", fmt.Errorf("instance: resolve source path %q: %w", src, err)
	}
	destination, err = filepath.Abs(dst)
	if err != nil {
		return "", "", fmt.Errorf("instance: resolve destination path %q: %w", dst, err)
	}
	source = filepath.Clean(source)
	destination = filepath.Clean(destination)
	if source == destination {
		return "", "", fmt.Errorf("instance: source and destination must differ")
	}
	if pathContains(source, destination) || pathContains(destination, source) {
		return "", "", fmt.Errorf("instance: source %q and destination %q must not overlap", source, destination)
	}
	return source, destination, nil
}

func ensureSourceDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("instance: inspect source directory %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("instance: source %q is not a directory", path)
	}
	return nil
}

func siblingTempPath(path, label string) string {
	base := fmt.Sprintf(".%s.rein-%s-%d", filepath.Base(path), label, time.Now().UnixNano())
	return filepath.Join(filepath.Dir(path), base)
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("instance: inspect %q: %w", path, err)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
