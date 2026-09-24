package wasmmod

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Resolver maps a module reference to a local .wasm path under modulesDir.
type Resolver struct {
	ModulesDir string
	// DigestMode controls per-resolve hashing: once (default) or always.
	DigestMode string
	cache      *digestCache
}

// NewResolver constructs a resolver. modulesDir is the host cache root
// (SB_WASM_MODULES_DIR).
func NewResolver(modulesDir string) *Resolver {
	return &Resolver{
		ModulesDir: modulesDir,
		DigestMode: moduleDigestModeOnce,
		cache:      newDigestCache(moduleDigestModeOnce),
	}
}

// SetDigestMode switches verify-once vs always-hash behavior (SB_WASM_MODULE_DIGEST_MODE).
func (r *Resolver) SetDigestMode(mode string) {
	if r == nil {
		return
	}
	r.DigestMode = mode
	r.cache = newDigestCache(mode)
}

// Resolve turns ref into a local file path. Phase 2 accepts:
//   - absolute paths to .wasm files
//   - file:// URLs
//   - bare filenames or relative paths under modulesDir
func (r *Resolver) Resolve(_ context.Context, ref string) (*ResolvedModule, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("module ref is required")
	}
	path, err := r.resolvePath(ref)
	if err != nil {
		return nil, err
	}
	if err := ValidateFile(path); err != nil {
		return nil, err
	}
	digest, size, err := r.digestFor(path)
	if err != nil {
		return nil, err
	}
	return &ResolvedModule{
		Ref:       ref,
		Path:      path,
		Digest:    digest,
		SizeBytes: size,
	}, nil
}

func (r *Resolver) resolvePath(ref string) (string, error) {
	if strings.HasPrefix(ref, "file://") {
		ref = strings.TrimPrefix(ref, "file://")
	}
	if filepath.IsAbs(ref) {
		return ref, nil
	}
	if r.ModulesDir == "" {
		return "", fmt.Errorf("relative module ref %q requires modules dir", ref)
	}
	joined := filepath.Join(r.ModulesDir, ref)
	// filepath.Join cleans ".." segments; a cleaned result outside
	// ModulesDir is a traversal attempt (e.g. "../../../etc/passwd").
	rel, err := filepath.Rel(r.ModulesDir, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q escapes the modules directory", ErrUnsafeModuleRef, ref)
	}
	return joined, nil
}

// IsHostPathRef reports whether ref names a host filesystem location
// (file:// URL or absolute path) rather than a name resolved under the
// modules directory or a registry ref. Callers on the API path use this to
// keep host-file access operator-only.
func IsHostPathRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	return strings.HasPrefix(ref, "file://") || filepath.IsAbs(ref)
}

func (r *Resolver) digestFor(path string) (hexDigest string, size int64, err error) {
	if r != nil && r.cache != nil {
		return r.cache.digestFor(path)
	}
	return fileDigest(path)
}

// InvalidateDigestCache drops verify-once entries for path after module delete/replace.
func (r *Resolver) InvalidateDigestCache(path string) {
	if r != nil && r.cache != nil {
		r.cache.dropPath(path)
	}
}
