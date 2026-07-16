//go:build darwin || linux

package platform

import (
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"syscall"
)

func ValidatePrivateDirectory(path string, purpose ValidationPurpose) error {
	if err := validatePurpose(purpose); err != nil {
		return err
	}
	validator, err := currentTrustValidator()
	if err != nil {
		return err
	}
	return validator.validatePrivateDirectory(path, purpose)
}

func ValidatePrivateFile(path string, purpose ValidationPurpose) error {
	if err := validatePurpose(purpose); err != nil {
		return err
	}
	validator, err := currentTrustValidator()
	if err != nil {
		return err
	}
	return validator.validatePrivateFile(path, purpose)
}

func ValidatePrivateSocket(path string, purpose ValidationPurpose) error {
	validator, err := currentTrustValidator()
	if err != nil {
		return err
	}
	return validator.validatePrivateSocket(path, purpose)
}

func currentTrustValidator() (trustValidator, error) {
	euid := os.Geteuid()
	if euid < 0 {
		return trustValidator{}, fmt.Errorf("effective uid must be non-negative")
	}
	filesystem := osTrustFilesystem{}
	return trustValidator{
		goos:       runtime.GOOS,
		euid:       euid,
		filesystem: filesystem,
		acls:       newPlatformACLValidator(),
	}, nil
}

type osTrustFilesystem struct{}

func (osTrustFilesystem) lstat(path string) (trustMetadata, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return trustMetadata{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return trustMetadata{}, fmt.Errorf("lstat for %q returned unsupported metadata", path)
	}
	return trustMetadata{mode: info.Mode(), uid: int(stat.Uid)}, nil
}

func (osTrustFilesystem) readlink(path string) (string, error) {
	return os.Readlink(path)
}

var _ trustFilesystem = osTrustFilesystem{}
var _ fs.FileInfo
