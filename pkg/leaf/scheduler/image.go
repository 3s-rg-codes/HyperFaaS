package scheduler

import (
	"strings"

	"hyperfaas-ideal-arch/pkg/core"
)

// WorkerHasCachedImage reports whether worker advertises want (tag, name, or digest).
// Matching prefers CachedImage.digest when set, then exact image ref equality.
func WorkerHasCachedImage(worker *core.WorkerState, want string) bool {
	want = strings.TrimSpace(want)
	if worker == nil || want == "" {
		return false
	}
	wantDigest := digestFromRef(want)
	for _, img := range worker.GetCachedImages() {
		if img == nil {
			continue
		}
		if dig := strings.TrimSpace(img.GetDigest()); dig != "" {
			if dig == want || dig == wantDigest || strings.HasSuffix(want, "@"+dig) {
				return true
			}
		}
		if ref := strings.TrimSpace(img.GetImage()); ref != "" {
			if ref == want {
				return true
			}
			if wantDigest != "" && (ref == wantDigest || digestFromRef(ref) == wantDigest) {
				return true
			}
		}
	}
	return false
}

func digestFromRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "sha256:") {
		return ref
	}
	if i := strings.LastIndex(ref, "@"); i >= 0 && i+1 < len(ref) {
		d := ref[i+1:]
		if strings.HasPrefix(d, "sha256:") {
			return d
		}
	}
	return ""
}

// FunctionImageRef returns the image string used for placement matching.
func FunctionImageRef(function *core.FunctionSpec) string {
	if function == nil || function.GetRuntime() == nil {
		return ""
	}
	return strings.TrimSpace(function.GetRuntime().GetImage())
}
