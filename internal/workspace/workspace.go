// @index Workspace filesystem service that owns namespace path validation, traversal-safe resolution, and bulk upload/delete operations.
package workspace

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Default upload size limits applied when callers do not provide their own Limits.
// @intent expose stable defaults so MCP handlers and other callers stay aligned with historical limits.
const (
	DefaultMaxFileBytes         = 10 << 20 // 10 MB
	DefaultMaxRequestBytes      = 50 << 20 // 50 MB
	DefaultMaxTotalDecodedBytes = 20 << 20 // 20 MB
)

// Limits bounds workspace upload payload sizes.
// @intent let MCP handlers and other callers configure upload caps while sharing the same enforcement code path.
type Limits struct {
	MaxFileBytes         int
	MaxRequestBytes      int
	MaxTotalDecodedBytes int
}

// DefaultLimits returns the historical workspace upload limits.
// @intent provide a single canonical default so handler code does not duplicate magic numbers.
func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:         DefaultMaxFileBytes,
		MaxRequestBytes:      DefaultMaxRequestBytes,
		MaxTotalDecodedBytes: DefaultMaxTotalDecodedBytes,
	}
}

// Service performs workspace filesystem operations under a single root.
// @intent encapsulate workspace path validation, traversal protection, and atomic file writes for handlers and CLIs.
type Service struct {
	// Root is the unresolved configured filesystem root for namespace storage.
	Root string
	// Limits bounds the size of accepted upload payloads.
	Limits Limits
}

// NewService constructs a Service for the supplied root.
// @intent build a workspace service with the canonical default limits applied when callers omit them.
func NewService(root string) *Service {
	return &Service{Root: workspaceRootOrDefault(root), Limits: DefaultLimits()}
}

// @intent collapse the empty-root convention used historically into a single helper.
func workspaceRootOrDefault(root string) string {
	if root == "" {
		return "workspaces"
	}
	return root
}

// SafeRoot returns the absolute, symlink-resolved workspace root, creating the root directory if needed.
// @intent ensure all workspace operations resolve paths under a trusted, real filesystem location.
// @sideEffect creates the workspace root directory when it does not yet exist.
func (s *Service) SafeRoot() (string, error) {
	root := workspaceRootOrDefault(s.Root)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return "", fmt.Errorf("create workspace root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	return realRoot, nil
}

// ResolvePath resolves a workspace-relative file path under the trusted workspace root.
// @intent reject path traversal and symlink escapes before any filesystem mutation reaches the path.
// @param namespace single-segment workspace name.
// @param filePath relative path inside the workspace ("" returns the workspace dir).
// @param allowMissingLeaf when true, allow the leaf to not yet exist (used before atomic writes).
func (s *Service) ResolvePath(namespace, filePath string, allowMissingLeaf bool) (string, error) {
	if err := ValidatePath(namespace, filePath); err != nil {
		return "", err
	}
	root, err := s.SafeRoot()
	if err != nil {
		return "", err
	}
	wsDir, err := EnsureNoSymlinkInPath(root, filepath.Clean(namespace), false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			wsDir = filepath.Join(root, filepath.Clean(namespace))
		} else {
			return "", err
		}
	}
	if filePath == "" {
		return wsDir, nil
	}
	rel := filepath.Join(filepath.Clean(namespace), filepath.Clean(filePath))
	return EnsureNoSymlinkInPath(root, rel, allowMissingLeaf)
}

// ValidatePath rejects empty, absolute, or traversal-bearing namespace and file inputs.
// @intent block path traversal attacks against workspace-scoped tools.
// @domainRule namespace must be a single safe path segment with no separators or parent-references.
func ValidatePath(namespace, filePath string) error {
	if namespace == "" {
		return fmt.Errorf("workspace must not be empty")
	}
	cleanWS := filepath.Clean(namespace)
	if cleanWS == "." || cleanWS == ".." || filepath.IsAbs(cleanWS) || strings.HasPrefix(cleanWS, "..") || strings.ContainsAny(cleanWS, `/\\`) {
		return fmt.Errorf("invalid workspace: must be a single safe name")
	}
	if filePath != "" {
		cleanFP := filepath.Clean(filePath)
		if filepath.IsAbs(cleanFP) || strings.HasPrefix(cleanFP, "..") {
			return fmt.Errorf("invalid file_path: path traversal not allowed")
		}
	}
	return nil
}

// EnsureNoSymlinkInPath walks each path segment from root to relPath rejecting symlinks.
// @intent prevent symlink traversal from escaping the workspace root before any filesystem mutation.
// @param allowMissingLeaf when true, returns the joined path even when the leaf does not yet exist.
func EnsureNoSymlinkInPath(root, relPath string, allowMissingLeaf bool) (string, error) {
	cleanRel := filepath.Clean(relPath)
	if cleanRel == "." {
		return root, nil
	}
	current := root
	segments := strings.Split(cleanRel, string(filepath.Separator))
	for i, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if allowMissingLeaf && errors.Is(err, fs.ErrNotExist) && i == len(segments)-1 {
				return current, nil
			}
			if allowMissingLeaf && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink paths are not allowed")
		}
	}
	return current, nil
}

// SafeWrite atomically writes data to path using a temp file and rename, refusing to follow symlinks.
// @intent guarantee partial writes are never visible as the final file contents.
// @sideEffect creates a temp file in the destination directory and renames it into place.
func SafeWrite(path string, data []byte, perm os.FileMode) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := writeFileNoFollow(tmpPath, data, perm); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// UploadRequest describes one file payload destined for a workspace.
// @intent provide a typed input shared by single and bulk upload paths.
type UploadRequest struct {
	Namespace string
	FilePath  string
	// ContentBase64 is the base64-encoded payload as supplied by callers.
	ContentBase64 string
}

// UploadResult reports the outcome of one accepted file upload.
// @intent expose the decoded byte size so handlers can report it back to callers.
type UploadResult struct {
	Namespace string
	FilePath  string
	Size      int
}

// ValidationError is returned for caller-fixable input issues so handlers can map them to user errors.
// @intent distinguish bad-input errors from genuine I/O or system errors at the API boundary.
type ValidationError struct {
	msg string
}

// Error returns the validation message.
// @intent expose the caller-fixable validation message without wrapping it in transport-specific formatting.
// @return returns the original validation message stored on the error.
func (e *ValidationError) Error() string { return e.msg }

// IsValidationError reports whether err is a ValidationError.
// @intent allow handlers to map validation failures to MCP user-error responses without leaking other errors.
func IsValidationError(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

// newValidationError constructs a ValidationError for caller-fixable workspace input issues.
// @intent centralize creation of typed validation failures so handlers can recognize them consistently.
func newValidationError(msg string) *ValidationError { return &ValidationError{msg: msg} }

// UploadFile validates, decodes, and atomically writes a single workspace file.
// @intent provide the canonical single-file upload primitive shared with bulk uploads.
// @domainRule decoded payloads cannot exceed the configured MaxFileBytes.
// @sideEffect creates the destination directory and writes the file atomically.
func (s *Service) UploadFile(req UploadRequest) (*UploadResult, error) {
	prepared, err := s.prepareUpload(req, 0)
	if err != nil {
		return nil, err
	}
	if err := s.commitPrepared(prepared); err != nil {
		return nil, err
	}
	return &UploadResult{Namespace: req.Namespace, FilePath: req.FilePath, Size: len(prepared.decoded)}, nil
}

// preparedUpload holds validated upload data before it is written to disk.
// @intent split validation/decoding from filesystem mutation so bulk uploads can validate before committing files.
type preparedUpload struct {
	req     UploadRequest
	decoded []byte
	target  string
}

// prepareUpload validates one upload request, decodes its content, and resolves its destination path.
// @intent prepare a single upload for later commit while enforcing per-file and aggregate decoded-size limits.
// @param alreadyDecoded tracks the total decoded bytes accepted earlier in the same bulk request.
// @return returns a preparedUpload ready for commit when validation succeeds.
// @domainRule rejects missing fields, invalid paths, invalid base64, oversized files, and oversized aggregate payloads.
func (s *Service) prepareUpload(req UploadRequest, alreadyDecoded int) (*preparedUpload, error) {
	limits := s.limitsOrDefault()
	if req.Namespace == "" || req.FilePath == "" || req.ContentBase64 == "" {
		return nil, newValidationError("namespace, file_path, and content are required")
	}
	if err := ValidatePath(req.Namespace, req.FilePath); err != nil {
		return nil, &ValidationError{msg: err.Error()}
	}
	decoded, err := base64.StdEncoding.DecodeString(req.ContentBase64)
	if err != nil {
		return nil, newValidationError(fmt.Sprintf("invalid base64 content: %v", err))
	}
	if len(decoded) > limits.MaxFileBytes {
		return nil, newValidationError(fmt.Sprintf("file exceeds %d MB size limit", limits.MaxFileBytes>>20))
	}
	if alreadyDecoded+len(decoded) > limits.MaxTotalDecodedBytes {
		return nil, newValidationError(fmt.Sprintf("total decoded upload exceeds %d MB size limit", limits.MaxTotalDecodedBytes>>20))
	}
	target, err := s.ResolvePath(req.Namespace, req.FilePath, true)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	return &preparedUpload{req: req, decoded: decoded, target: target}, nil
}

// commitPrepared creates parent directories, revalidates the path, and atomically writes one prepared upload.
// @intent separate filesystem mutation from validation so bulk uploads can fail fast before writing any file.
// @sideEffect creates directories and writes the target file atomically.
func (s *Service) commitPrepared(p *preparedUpload) error {
	if err := os.MkdirAll(filepath.Dir(p.target), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if _, err := s.ResolvePath(p.req.Namespace, p.req.FilePath, true); err != nil {
		return fmt.Errorf("revalidate workspace path: %w", err)
	}
	if err := SafeWrite(p.target, p.decoded, 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// limitsOrDefault returns the configured limits with zero values replaced by workspace defaults.
// @intent ensure upload validation always runs with concrete byte ceilings even when callers omit custom limits.
// @return returns a Limits value whose file, request, and aggregate byte caps are all populated.
func (s *Service) limitsOrDefault() Limits {
	l := s.Limits
	if l.MaxFileBytes == 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	if l.MaxRequestBytes == 0 {
		l.MaxRequestBytes = DefaultMaxRequestBytes
	}
	if l.MaxTotalDecodedBytes == 0 {
		l.MaxTotalDecodedBytes = DefaultMaxTotalDecodedBytes
	}
	return l
}

// BulkEntry is the JSON envelope used by bulk uploads.
// @intent mirror the historical MCP request shape so handlers can deserialize directly.
type BulkEntry struct {
	Namespace string `json:"namespace"`
	Workspace string `json:"workspace"`
	FilePath  string `json:"file_path"`
	Content   string `json:"content"`
}

// BulkEntryError reports the index of the failing entry alongside the underlying error.
// @intent let handlers prefix MCP error messages with the offending entry index without re-implementing iteration.
type BulkEntryError struct {
	Index int
	Err   error
}

// Error formats the failing bulk entry index with the underlying error text.
// @intent preserve a user-facing error string that points callers to the exact bad entry.
// @return returns an error string prefixed with the failing entry index.
func (e *BulkEntryError) Error() string { return fmt.Sprintf("entry %d: %v", e.Index, e.Err) }

// Unwrap returns the underlying entry error.
// @intent allow callers to inspect the original validation or filesystem failure that broke a bulk upload.
// @return returns the underlying entry error for errors.Is and errors.As checks.
func (e *BulkEntryError) Unwrap() error { return e.Err }

// UploadFiles parses a bulk upload JSON payload, validates every entry, and writes them sequentially.
// @intent batch workspace uploads with the same validation rules as single uploads while keeping atomicity per file.
// @domainRule rejects the request when raw bytes exceed MaxRequestBytes, when the array is empty, or when any entry fails validation.
// @sideEffect creates directories and writes each accepted file atomically.
func (s *Service) UploadFiles(rawJSON string) ([]UploadResult, error) {
	limits := s.limitsOrDefault()
	if len(rawJSON) > limits.MaxRequestBytes {
		return nil, newValidationError(fmt.Sprintf("total upload request exceeds %d MB size limit", limits.MaxRequestBytes>>20))
	}
	var entries []BulkEntry
	if err := json.Unmarshal([]byte(rawJSON), &entries); err != nil {
		return nil, newValidationError(fmt.Sprintf("invalid files JSON: %v", err))
	}
	if len(entries) == 0 {
		return nil, newValidationError("files array must not be empty")
	}

	prepared := make([]*preparedUpload, 0, len(entries))
	totalDecoded := 0
	for i, e := range entries {
		ns := e.Namespace
		if ns == "" {
			ns = e.Workspace
		}
		req := UploadRequest{Namespace: ns, FilePath: e.FilePath, ContentBase64: e.Content}
		p, err := s.prepareUpload(req, totalDecoded)
		if err != nil {
			return nil, &BulkEntryError{Index: i, Err: err}
		}
		totalDecoded += len(p.decoded)
		prepared = append(prepared, p)
	}

	results := make([]UploadResult, 0, len(prepared))
	for i, p := range prepared {
		if err := s.commitPrepared(p); err != nil {
			return nil, &BulkEntryError{Index: i, Err: err}
		}
		results = append(results, UploadResult{Namespace: p.req.Namespace, FilePath: p.req.FilePath, Size: len(p.decoded)})
	}
	return results, nil
}

// ListNamespaces returns the alphabetically sorted directories under the workspace root.
// @intent expose available namespaces so callers can pick an upload target.
// @sideEffect reads the workspace root directory.
func (s *Service) ListNamespaces() ([]string, error) {
	entries, err := os.ReadDir(workspaceRootOrDefault(s.Root))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read namespace root: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

// ListFiles walks the namespace directory returning relative file paths sorted alphabetically.
// @intent surface the current file inventory of a namespace for clients that need to plan further operations.
// @sideEffect performs a recursive filesystem walk that skips symlinks defensively.
func (s *Service) ListFiles(namespace string) ([]string, error) {
	if err := ValidatePath(namespace, ""); err != nil {
		return nil, &ValidationError{msg: err.Error()}
	}
	wsDir, err := s.ResolvePath(namespace, "", false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("resolve namespace path: %w", err)
	}
	var files []string
	err = filepath.Walk(wsDir, func(fp string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(wsDir, fp)
		if relErr != nil {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk namespace: %w", err)
	}
	if files == nil {
		files = []string{}
	}
	slices.Sort(files)
	return files, nil
}

// DeleteFile removes a single namespaced file after path validation.
// @intent allow targeted cleanup of namespace contents without exposing raw filesystem paths.
// @sideEffect deletes the file from the filesystem.
func (s *Service) DeleteFile(namespace, filePath string) error {
	if err := ValidatePath(namespace, filePath); err != nil {
		return &ValidationError{msg: err.Error()}
	}
	target, err := s.ResolvePath(namespace, filePath, false)
	if err != nil {
		return fmt.Errorf("resolve namespace path: %w", err)
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return &ValidationError{msg: fmt.Sprintf("file %q not found in namespace %q", filePath, namespace)}
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

// ResolveExistingNamespace returns the validated namespace directory ensuring it currently exists.
// @intent let callers stat or remove an entire namespace without duplicating validation logic.
func (s *Service) ResolveExistingNamespace(namespace string) (string, error) {
	if err := ValidatePath(namespace, ""); err != nil {
		return "", &ValidationError{msg: err.Error()}
	}
	wsDir, err := s.ResolvePath(namespace, "", false)
	if err != nil {
		return "", fmt.Errorf("resolve namespace path: %w", err)
	}
	if _, err := os.Stat(wsDir); os.IsNotExist(err) {
		return "", &ValidationError{msg: fmt.Sprintf("namespace %q not found", namespace)}
	}
	return wsDir, nil
}

// RemoveTree recursively deletes a previously resolved namespace directory.
// @intent provide the final filesystem step of namespace deletion after upstream cleanup succeeds.
// @sideEffect recursively deletes the namespace directory tree.
func (s *Service) RemoveTree(wsDir string) error {
	if err := os.RemoveAll(wsDir); err != nil {
		return fmt.Errorf("delete namespace: %w", err)
	}
	return nil
}
