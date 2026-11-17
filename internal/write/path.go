package write

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath 把用户/Agent 提供的路径解析到工作区内，返回绝对路径与 slash 形式相对路径。
// 拒绝绝对路径逃逸、.. 逃逸、.git 以及符号链接逃逸。
func ResolvePath(root, p string) (abs, rel string, err error) {
	return resolveWithin(root, p, false)
}

// ResolveDir 解析一个必须已存在的工作区目录，用于 bash 的工作目录。
func ResolveDir(root, p string) (abs, rel string, err error) {
	return resolveWithin(root, p, true)
}

func resolveWithin(root, p string, requireDir bool) (abs, rel string, err error) {
	if strings.TrimSpace(p) == "" {
		return "", "", fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	if strings.ContainsRune(p, 0) {
		return "", "", fmt.Errorf("%w: NUL in path", ErrInvalidPath)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	rootAbs = filepath.Clean(rootAbs)

	clean := filepath.Clean(p)
	if filepath.IsAbs(clean) {
		abs = clean
	} else {
		abs = filepath.Join(rootAbs, clean)
	}
	abs = filepath.Clean(abs)

	rel, err = filepath.Rel(rootAbs, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%w: %s", ErrInvalidPath, p)
	}
	slashRel := filepath.ToSlash(rel)
	if slashRel == ".git" || strings.HasPrefix(slashRel, ".git/") {
		return "", "", fmt.Errorf("%w: writing under .git is forbidden", ErrInvalidPath)
	}

	if err := ensureNoSymlinkEscape(rootAbs, abs, requireDir); err != nil {
		return "", "", err
	}
	return abs, slashRel, nil
}

func ensureNoSymlinkEscape(rootAbs, abs string, requireDir bool) error {
	// 目标本身若是符号链接，拒绝写入，避免写到工作区外。
	if fi, err := os.Lstat(abs); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: target is a symlink", ErrInvalidPath)
		}
		if requireDir && !fi.IsDir() {
			return fmt.Errorf("%w: workdir is not a directory", ErrInvalidPath)
		}
		if !requireDir && fi.IsDir() {
			return fmt.Errorf("%w: target is a directory", ErrInvalidPath)
		}
	} else if os.IsNotExist(err) && requireDir {
		return fmt.Errorf("%w: workdir does not exist", ErrInvalidPath)
	} else if !os.IsNotExist(err) {
		return err
	}

	// 找到最近的存在祖先，解析符号链接后必须仍在 root 内。
	parent := filepath.Dir(abs)
	probe := parent
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		next := filepath.Dir(probe)
		if next == probe {
			return fmt.Errorf("%w: cannot resolve parent", ErrInvalidPath)
		}
		probe = next
	}
	eval, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	if !IsWithin(rootAbs, eval) {
		return fmt.Errorf("%w: symlink parent escapes workspace", ErrInvalidPath)
	}
	return nil
}

// IsWithin 判断 abs 是否在 root 内（含相等）。
func IsWithin(rootAbs, abs string) bool {
	rel, err := filepath.Rel(filepath.Clean(rootAbs), filepath.Clean(abs))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// ReadFileLimited 读取工作区文本文件，超过 max 字节返回 ErrTooLarge。
func ReadFileLimited(abs string, max int64) (string, error) {
	fi, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: target is a symlink", ErrInvalidPath)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%w: target is a directory", ErrInvalidPath)
	}
	if fi.Size() > max {
		return "", ErrTooLarge
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if int64(len(b)) > max {
		return "", ErrTooLarge
	}
	return string(b), nil
}
