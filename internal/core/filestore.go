package core

// FileStore abstracts every file operation the apply pipeline needs,
// beyond the read-only questions of FileProbe. Production uses the OS
// implementation in internal/platform; pipeline tests use an
// in-memory fake, so backup/rollback logic is proven without a disk.
type FileStore interface {
	FileProbe

	// ReadFile returns a file's contents (error when missing).
	ReadFile(path string) ([]byte, error)
	// WriteFile writes contents, creating parent dirs as needed.
	WriteFile(path string, data []byte) error
	// WriteFileAtomic writes via a temp file in the same folder plus
	// rename, so a crash never leaves a half-written registry/config.
	WriteFileAtomic(path string, data []byte) error
	// MkdirAll creates a directory tree.
	MkdirAll(dir string) error
	// Remove deletes one file (missing file is not an error).
	Remove(path string) error
	// RemoveAll deletes a file or directory tree (missing is fine).
	RemoveAll(path string) error
	// ListNames returns the entry names inside dir (empty when the
	// directory does not exist).
	ListNames(dir string) ([]string, error)
}
