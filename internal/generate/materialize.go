package generate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// applyPlan writes changed artifacts atomically, deletes removed ones, and
// prunes any directories left empty.
func applyPlan(outDir string, p plan) error {
	for _, a := range p.write {
		full := filepath.Join(outDir, filepath.FromSlash(a.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(full, a.Bytes, 0o644); err != nil {
			return err
		}
	}
	for _, path := range p.deleteP {
		full := filepath.Join(outDir, filepath.FromSlash(path))
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	pruneEmptyDirs(outDir, p.deleteP)
	return nil
}

// writeFileAtomic writes b to a same-dir temp file, fsyncs it, and renames it
// into place so a reader never sees a partial file.
func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".cnz-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err = os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// sweepTempFiles removes leftover ".cnz-*" temp files anywhere under outDir (an
// interrupted prior run). It tolerates a missing outDir.
func sweepTempFiles(outDir string) error {
	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(outDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".cnz-") {
			return os.Remove(p)
		}
		return nil
	})
}

// pruneEmptyDirs walks each deleted path's parent chain bottom-up, removing
// now-empty directories, stopping at outDir.
func pruneEmptyDirs(outDir string, deleted []string) {
	cleanOut := filepath.Clean(outDir)
	seen := map[string]bool{}
	for _, p := range deleted {
		dir := filepath.Dir(filepath.Join(cleanOut, filepath.FromSlash(p)))
		for {
			if dir == cleanOut || !strings.HasPrefix(dir, cleanOut+string(os.PathSeparator)) {
				break
			}
			if seen[dir] {
				break
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) > 0 {
				break
			}
			if err := os.Remove(dir); err != nil {
				break
			}
			seen[dir] = true
			dir = filepath.Dir(dir)
		}
	}
}
