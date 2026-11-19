//go:build linux

package workloadspawn

import (
	"os"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
)

// Qualification helpers are available only to external tests, never shipping APIs.
func QualificationStoreForTest(t *testing.T) *artifactfs.Store { return newQualificationStore(t) }
func SealRuntimeForTest(t *testing.T, s *artifactfs.Store, dir, lang string) string {
	return sealQualificationRuntime(t, s, dir, lang, true)
}
func SealCodeForTest(t *testing.T, s *artifactfs.Store, owner, entry, source, lazyName, lazy string) (string, string) {
	return sealQualificationCode(t, s, owner, entry, source, lazyName, lazy)
}
func ReclaimForTest(t *testing.T, s *artifactfs.Store, id string) { reclaimQualified(t, s, id) }
func VerifiedPackageForTest(path, sha string) (*os.File, error) {
	return verifiedRuntimePackage(path, sha)
}
