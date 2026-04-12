package validators

import (
	"fmt"
	"strings"

	"collaborative/internal/model"
)

// AuthValidator validates authentication input
type AuthValidator struct{}

func NewAuthValidator() *AuthValidator {
	return &AuthValidator{}
}

func (v *AuthValidator) ValidateLogin(login string) error {
	if len(login) < 3 {
		return fmt.Errorf("login must be at least 3 characters")
	}
	if len(login) > 50 {
		return fmt.Errorf("login must be at most 50 characters")
	}
	return nil
}

func (v *AuthValidator) ValidatePassword(password string) error {
	if len(password) < 6 {
		return fmt.Errorf("password must be at least 6 characters")
	}
	if len(password) > 100 {
		return fmt.Errorf("password must be at most 100 characters")
	}
	return nil
}

// ConfigValidator validates processing configuration
type ConfigValidator struct{}

func NewConfigValidator() *ConfigValidator {
	return &ConfigValidator{}
}

func (v *ConfigValidator) ValidateProcessingConfig(config *model.UserProcessingConfig) error {
	switch config.Method {
	case model.MethodSingle, model.MethodRelative, model.MethodPPP:
		// valid
	default:
		return fmt.Errorf("invalid processing method: %q, must be one of: single, relative, ppp", config.Method)
	}

	if config.Mode != "" {
		switch config.Mode {
		case model.ModeKinematic, model.ModeStatic:
			// valid
		default:
			return fmt.Errorf("invalid processing mode: %q, must be one of: kinematic, static", config.Mode)
		}
	}

	if config.ElevationMask < 0 || config.ElevationMask > 90 {
		return fmt.Errorf("elevation mask must be between 0 and 90 degrees, got %.1f", config.ElevationMask)
	}

	return nil
}

// FileValidator validates uploaded files
type FileValidator struct {
	maxSize int64
}

func NewFileValidator() *FileValidator {
	return &FileValidator{maxSize: 100 * 1024 * 1024 * 1024} // 1 GB
}

var validExtensions = []string{".obs", ".rnx", ".crx", ".gz", ".o"}

func (v *FileValidator) ValidateFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("filename is required")
	}

	lower := strings.ToLower(filename)
	for _, ext := range validExtensions {
		if strings.HasSuffix(lower, ext) {
			return nil
		}
	}

	return fmt.Errorf("unsupported file format, supported: %s", strings.Join(validExtensions, ", "))
}

func (v *FileValidator) ValidateFileSize(size int64) error {
	if size == 0 {
		return fmt.Errorf("file is empty")
	}
	if size > v.maxSize {
		return fmt.Errorf("file too large: %d bytes, maximum is %d MB", size, v.maxSize/(1024*1024))
	}
	return nil
}

// PaginationValidator validates pagination parameters
type PaginationValidator struct {
	maxLimit  int
	maxOffset int
}

func NewPaginationValidator(maxLimit, maxOffset int) *PaginationValidator {
	return &PaginationValidator{maxLimit: maxLimit, maxOffset: maxOffset}
}

func (v *PaginationValidator) ValidateLimitOffset(limit, offset int) (int, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > v.maxLimit {
		limit = v.maxLimit
	}
	if offset < 0 {
		offset = 0
	}
	if offset > v.maxOffset {
		offset = v.maxOffset
	}
	return limit, offset, nil
}
