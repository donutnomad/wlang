package go2wlang

import (
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type packageNameResolver struct {
	projectRoot string
}

func newPackageNameResolver(startDir string) *packageNameResolver {
	return &packageNameResolver{projectRoot: findGoModDir(startDir)}
}

func (r *packageNameResolver) packageName(importPath string) (string, error) {
	diskPath, err := r.resolveDiskPath(importPath)
	if err != nil {
		return "", err
	}
	return readPackageName(diskPath)
}

func (r *packageNameResolver) resolveDiskPath(importPath string) (string, error) {
	if importPath == "" {
		return "", fmt.Errorf("empty import path")
	}
	if stdPath, ok := stdlibPath(importPath); ok {
		return stdPath, nil
	}
	if r.projectRoot != "" {
		if diskPath, ok := r.projectImportPath(importPath); ok {
			return diskPath, nil
		}
		if diskPath, ok := r.replacedImportPath(importPath); ok {
			return diskPath, nil
		}
		if diskPath, ok := r.workspaceImportPath(importPath); ok {
			return diskPath, nil
		}
		if diskPath, ok := vendorImportPath(r.projectRoot, importPath); ok {
			return diskPath, nil
		}
	}
	if diskPath, ok := moduleCacheImportPath(importPath); ok {
		return diskPath, nil
	}
	if diskPath, ok := gopathImportPath(importPath); ok {
		return diskPath, nil
	}
	return "", fmt.Errorf("package %s not found", importPath)
}

func (r *packageNameResolver) projectImportPath(importPath string) (string, bool) {
	moduleName, err := moduleName(r.projectRoot)
	if err != nil || !importPathInModule(importPath, moduleName) {
		return "", false
	}
	diskPath := filepath.Join(r.projectRoot, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(importPath, moduleName), "/")))
	return existingDir(diskPath)
}

func (r *packageNameResolver) replacedImportPath(importPath string) (string, bool) {
	for _, repl := range parseGoModReplaces(filepath.Join(r.projectRoot, "go.mod")) {
		if !importPathInModule(importPath, repl.oldPath) {
			continue
		}
		if strings.Contains(repl.newPath, "://") || strings.Contains(repl.newPath, "@") {
			continue
		}
		base := repl.newPath
		if !filepath.IsAbs(base) {
			base = filepath.Join(r.projectRoot, base)
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(importPath, repl.oldPath), "/")
		if diskPath, ok := existingDir(filepath.Join(base, filepath.FromSlash(rel))); ok {
			return diskPath, true
		}
	}
	return "", false
}

func (r *packageNameResolver) workspaceImportPath(importPath string) (string, bool) {
	goWorkDir := findGoWorkDir(r.projectRoot)
	if goWorkDir == "" {
		return "", false
	}
	for _, useDir := range parseGoWorkUseDirs(filepath.Join(goWorkDir, "go.work")) {
		absDir := useDir
		if !filepath.IsAbs(absDir) {
			absDir = filepath.Join(goWorkDir, absDir)
		}
		modName, err := moduleName(absDir)
		if err != nil || !importPathInModule(importPath, modName) {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(importPath, modName), "/")
		if diskPath, ok := existingDir(filepath.Join(absDir, filepath.FromSlash(rel))); ok {
			return diskPath, true
		}
	}
	return "", false
}

type replaceDirective struct {
	oldPath string
	newPath string
}

func parseGoModReplaces(filename string) []replaceDirective {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	out := []replaceDirective{}
	inBlock := false
	for _, line := range lines {
		line = stripLineComment(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "replace") && strings.Contains(line, "(") {
			inBlock = true
			continue
		}
		if inBlock {
			if strings.Contains(line, ")") {
				inBlock = false
				continue
			}
			if repl, ok := parseReplaceLine(line); ok {
				out = append(out, repl)
			}
			continue
		}
		if strings.HasPrefix(line, "replace ") {
			if repl, ok := parseReplaceLine(strings.TrimSpace(strings.TrimPrefix(line, "replace"))); ok {
				out = append(out, repl)
			}
		}
	}
	return out
}

func parseReplaceLine(line string) (replaceDirective, bool) {
	before, after, ok := strings.Cut(line, "=>")
	if !ok {
		return replaceDirective{}, false
	}
	left := strings.Fields(strings.TrimSpace(before))
	right := strings.Fields(strings.TrimSpace(after))
	if len(left) == 0 || len(right) == 0 {
		return replaceDirective{}, false
	}
	return replaceDirective{oldPath: left[0], newPath: right[0]}, true
}

func parseGoWorkUseDirs(filename string) []string {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	out := []string{}
	inBlock := false
	for _, line := range lines {
		line = stripLineComment(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "use") && strings.Contains(line, "(") {
			inBlock = true
			continue
		}
		if inBlock {
			if strings.Contains(line, ")") {
				inBlock = false
				continue
			}
			out = append(out, strings.Fields(line)[0])
			continue
		}
		if strings.HasPrefix(line, "use ") {
			fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "use")))
			if len(fields) > 0 && fields[0] != "(" {
				out = append(out, fields[0])
			}
		}
	}
	return out
}

func stripLineComment(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return line
}

func moduleName(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = stripLineComment(strings.TrimSpace(line))
		if strings.HasPrefix(line, "module ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1], nil
			}
		}
	}
	return "", fmt.Errorf("module declaration not found in %s", filepath.Join(root, "go.mod"))
}

func importPathInModule(importPath, moduleName string) bool {
	return importPath == moduleName || strings.HasPrefix(importPath, moduleName+"/")
}

func findGoModDir(startDir string) string {
	if startDir == "" {
		return ""
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		dir = startDir
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func findGoWorkDir(startDir string) string {
	if startDir == "" {
		return ""
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		dir = startDir
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func stdlibPath(importPath string) (string, bool) {
	first, _, _ := strings.Cut(importPath, "/")
	if strings.Contains(first, ".") {
		return "", false
	}
	goroot := build.Default.GOROOT
	if goroot == "" {
		goroot = os.Getenv("GOROOT")
	}
	if goroot == "" {
		return "", false
	}
	return existingDir(filepath.Join(goroot, "src", filepath.FromSlash(importPath)))
}

func vendorImportPath(projectRoot, importPath string) (string, bool) {
	return existingDir(filepath.Join(projectRoot, "vendor", filepath.FromSlash(importPath)))
}

func moduleCacheImportPath(importPath string) (string, bool) {
	goModCache := os.Getenv("GOMODCACHE")
	if goModCache == "" {
		goPath := os.Getenv("GOPATH")
		if goPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", false
			}
			goPath = filepath.Join(home, "go")
		}
		goModCache = filepath.Join(goPath, "pkg", "mod")
	}
	parts := strings.Split(importPath, "/")
	for i := len(parts); i >= 1; i-- {
		modulePath := strings.Join(parts[:i], "/")
		subPath := strings.Join(parts[i:], "/")
		matches, err := filepath.Glob(filepath.Join(goModCache, encodeModulePath(modulePath)+"@*"))
		if err != nil || len(matches) == 0 {
			continue
		}
		for j := len(matches) - 1; j >= 0; j-- {
			diskPath := matches[j]
			if subPath != "" {
				diskPath = filepath.Join(diskPath, filepath.FromSlash(subPath))
			}
			if diskPath, ok := existingDir(diskPath); ok {
				return diskPath, true
			}
		}
	}
	return "", false
}

func gopathImportPath(importPath string) (string, bool) {
	goPath := os.Getenv("GOPATH")
	if goPath == "" {
		return "", false
	}
	return existingDir(filepath.Join(goPath, "src", filepath.FromSlash(importPath)))
}

func encodeModulePath(modulePath string) string {
	var b strings.Builder
	for _, r := range modulePath {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func existingDir(dir string) (string, bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

func readPackageName(pkgDir string) (string, error) {
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		matched, err := build.Default.MatchFile(pkgDir, name)
		if err != nil || !matched {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(pkgDir, name), nil, parser.PackageClauseOnly)
		if err != nil {
			continue
		}
		if file.Name != nil && file.Name.Name != "" {
			return file.Name.Name, nil
		}
	}
	return "", fmt.Errorf("package declaration not found in %s", pkgDir)
}
