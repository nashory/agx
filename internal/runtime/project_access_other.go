//go:build !darwin

package runtime

func repairProjectAccessPlatform(path string) error {
	return validateProjectAccess(path)
}
