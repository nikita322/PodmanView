package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"podmanview/internal/auth"
	"podmanview/internal/events"
	"podmanview/internal/logger"
)

// Global constants for MIME type detection (performance optimization)
var mimeTypesByExtension = map[string]string{
	// Images
	".svg":  "image/svg+xml",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".avif": "image/avif",

	// Video
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".ogg":  "video/ogg",
	".avi":  "video/x-msvideo",
	".mov":  "video/quicktime",
	".mkv":  "video/x-matroska",

	// Audio
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".aac":  "audio/aac",
	".oga":  "audio/ogg",

	// Documents
	".pdf": "application/pdf",
	".zip": "application/zip",
	".tar": "application/x-tar",
	".gz":  "application/gzip",
	".rar": "application/x-rar-compressed",
	".7z":  "application/x-7z-compressed",

	// Text/Code
	".json": "application/json",
	".xml":  "application/xml",
	".html": "text/html",
	".css":  "text/css",
	".js":   "text/javascript",
	".txt":  "text/plain",
}

var binaryMimePrefixes = []string{
	"image/",
	"video/",
	"audio/",
	"application/pdf",
	"application/zip",
	"application/x-tar",
	"application/gzip",
	"application/x-rar",
	"application/x-7z-compressed",
	"application/octet-stream",
	"font/",
}

var binaryExtensions = []string{
	".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".ico", ".svg",
	".mp4", ".webm", ".ogg", ".avi", ".mov", ".mkv",
	".mp3", ".wav", ".flac", ".aac",
	".pdf",
	".zip", ".tar", ".gz", ".rar", ".7z",
	".ttf", ".otf", ".woff", ".woff2",
	".exe", ".dll", ".so", ".dylib",
}

// FileManagerHandler handles file operations
type FileManagerHandler struct {
	eventStore    *events.Store
	baseDir       string // Base directory for file operations (e.g., /home)
	maxUploadSize int64  // Maximum upload size in bytes (default 100MB)
	pathCache     *pathValidationCache
	logger        *logger.Logger
}

// pathValidationCache caches validated paths to avoid repeated validation
type pathValidationCache struct {
	sync.RWMutex
	cache map[string]string // requestPath -> absPath
	maxSize int
}

// NewFileManagerHandler creates new file manager handler
func NewFileManagerHandler(eventStore *events.Store, baseDir string, logger *logger.Logger) *FileManagerHandler {
	// If baseDir is empty, use user's home directory or root
	if baseDir == "" {
		if homeDir, err := os.UserHomeDir(); err == nil {
			baseDir = homeDir
		} else {
			baseDir = "/"
		}
	}

	// Clean and resolve absolute path
	baseDir, err := filepath.Abs(baseDir)
	if err != nil {
		if logger != nil {
			logger.Printf("Warning: failed to resolve base directory, using /: %v", err)
		}
		baseDir = "/"
	}

	return &FileManagerHandler{
		eventStore:    eventStore,
		baseDir:       baseDir,
		maxUploadSize: 100 * 1024 * 1024, // 100MB default
		pathCache: &pathValidationCache{
			cache:   make(map[string]string),
			maxSize: 1000, // Cache up to 1000 paths
		},
		logger: logger,
	}
}

// newPathValidationCache creates a new path validation cache
func (c *pathValidationCache) get(key string) (string, bool) {
	c.RLock()
	defer c.RUnlock()
	val, ok := c.cache[key]
	return val, ok
}

// set stores a validated path in cache
func (c *pathValidationCache) set(key, value string) {
	c.Lock()
	defer c.Unlock()

	// Simple LRU: if cache is full, clear half of it
	if len(c.cache) >= c.maxSize {
		// Clear half the cache (simple eviction strategy)
		count := 0
		for k := range c.cache {
			delete(c.cache, k)
			count++
			if count >= c.maxSize/2 {
				break
			}
		}
	}

	c.cache[key] = value
}

// FileInfo represents file or directory information
type FileInfo struct {
	Name    string    `json:"name"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Mode    string    `json:"mode"`
	Path    string    `json:"path"` // Full path relative to baseDir
}

// BrowseResponse represents directory browsing response
type BrowseResponse struct {
	Path       string     `json:"path"`        // Current path relative to baseDir
	Parent     string     `json:"parent"`      // Parent directory path
	Items      []FileInfo `json:"items"`       // Files and directories
	TotalCount int        `json:"total_count"` // Total number of items in directory
	Offset     int        `json:"offset"`      // Current offset
	Limit      int        `json:"limit"`       // Items per page
	HasMore    bool       `json:"has_more"`    // Whether there are more items
}

// validatePath checks if path is safe and within baseDir
// Returns cleaned absolute path and error
// Uses cache to avoid repeated validation of same paths
func (h *FileManagerHandler) validatePath(requestedPath string) (string, error) {
	// Special case: "/" or empty path means baseDir root
	if requestedPath == "" || requestedPath == "/" {
		return h.baseDir, nil
	}

	// Check cache first
	if cachedPath, ok := h.pathCache.get(requestedPath); ok {
		return cachedPath, nil
	}

	// Clean the path to remove .. and other unsafe elements
	cleanPath := filepath.Clean(requestedPath)

	// Remove leading slash for relative path construction
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	cleanPath = strings.TrimPrefix(cleanPath, "\\")

	// Join with baseDir to get absolute path
	absPath := filepath.Join(h.baseDir, cleanPath)

	// Resolve to absolute path (handles symlinks)
	absPath, err := filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	// Ensure path is within baseDir (prevent path traversal)
	// Use filepath.Clean to normalize both paths for comparison
	normalizedBase := filepath.Clean(h.baseDir)
	normalizedAbs := filepath.Clean(absPath)

	if !strings.HasPrefix(normalizedAbs, normalizedBase) {
		return "", fmt.Errorf("access denied: path outside base directory")
	}

	// Cache the validated path
	h.pathCache.set(requestedPath, absPath)

	return absPath, nil
}

// getRelativePath returns path relative to baseDir
func (h *FileManagerHandler) getRelativePath(absPath string) string {
	relPath, err := filepath.Rel(h.baseDir, absPath)
	if err != nil {
		return "/"
	}
	if relPath == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(relPath)
}

// Browse lists files and directories with pagination
func (h *FileManagerHandler) Browse(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	requestedPath := r.URL.Query().Get("path")
	if requestedPath == "" {
		requestedPath = "/"
	}

	// Parse pagination parameters
	offset := 0
	limit := 500 // Default: 500 items per page
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}

	// Validate and get absolute path
	absPath, err := h.validatePath(requestedPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if path exists and is a directory
	stat, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "Directory not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to access directory", http.StatusInternalServerError)
		}
		return
	}

	if !stat.IsDir() {
		http.Error(w, "Path is not a directory", http.StatusBadRequest)
		return
	}

	// Read directory contents
	entries, err := os.ReadDir(absPath)
	if err != nil {
		http.Error(w, "Failed to read directory", http.StatusInternalServerError)
		h.logger.Printf("Failed to read directory %s: %v", absPath, err)
		return
	}

	totalCount := len(entries)

	// Apply pagination
	start := offset
	end := offset + limit
	if start > totalCount {
		start = totalCount
	}
	if end > totalCount {
		end = totalCount
	}

	paginatedEntries := entries[start:end]

	// Build file list for current page
	items := make([]FileInfo, 0, len(paginatedEntries))
	for _, entry := range paginatedEntries {
		info, err := entry.Info()
		if err != nil {
			continue // Skip files we can't stat
		}

		itemPath := filepath.Join(absPath, entry.Name())
		relPath := h.getRelativePath(itemPath)

		items = append(items, FileInfo{
			Name:    entry.Name(),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Mode:    info.Mode().String(),
			Path:    relPath,
		})
	}

	// Get parent directory
	parentPath := "/"
	if absPath != h.baseDir {
		parentAbs := filepath.Dir(absPath)
		parentPath = h.getRelativePath(parentAbs)
	}

	response := BrowseResponse{
		Path:       h.getRelativePath(absPath),
		Parent:     parentPath,
		Items:      items,
		TotalCount: totalCount,
		Offset:     offset,
		Limit:      limit,
		HasMore:    end < totalCount,
	}

	// Log browse event
	h.eventStore.Add(events.EventFileBrowse, user.Username, getClientIP(r), true,
		fmt.Sprintf("path=%s items=%d/%d", h.getRelativePath(absPath), len(items), totalCount))

	writeJSON(w, http.StatusOK, response)
}

// Download serves a file for download
func (h *FileManagerHandler) Download(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	requestedPath := r.URL.Query().Get("path")
	if requestedPath == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Validate and get absolute path
	absPath, err := h.validatePath(requestedPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if file exists
	stat, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to access file", http.StatusInternalServerError)
		}
		return
	}

	if stat.IsDir() {
		http.Error(w, "Cannot download directory", http.StatusBadRequest)
		return
	}

	// Open file
	file, err := os.Open(absPath)
	if err != nil {
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		h.logger.Printf("Failed to open file %s: %v", absPath, err)
		return
	}
	defer file.Close()

	// Detect content type
	contentType := mime.TypeByExtension(filepath.Ext(absPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Set headers
	w.Header().Set("Content-Type", contentType)
	// Sanitize filename to prevent HTTP header injection
	safeFilename := sanitizeFilename(filepath.Base(absPath))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", safeFilename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))

	// Stream file to client
	_, err = io.Copy(w, file)
	if err != nil {
		h.logger.Printf("Failed to send file %s: %v", absPath, err)
		return
	}

	// Log download event
	h.eventStore.Add(events.EventFileDownload, user.Username, getClientIP(r), true,
		fmt.Sprintf("file=%s size=%d", filepath.Base(absPath), stat.Size()))
}

// Upload handles file uploads (multipart form)
func (h *FileManagerHandler) Upload(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadSize)

	// Parse multipart form
	err := r.ParseMultipartForm(h.maxUploadSize)
	if err != nil {
		http.Error(w, "File too large or invalid form data", http.StatusBadRequest)
		return
	}

	// Get target directory
	targetPath := r.FormValue("path")
	if targetPath == "" {
		targetPath = "/"
	}

	// Validate target directory
	absTargetDir, err := h.validatePath(targetPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if target is a directory
	stat, err := os.Stat(absTargetDir)
	if err != nil || !stat.IsDir() {
		http.Error(w, "Target path is not a directory", http.StatusBadRequest)
		return
	}

	// Get uploaded files
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	uploadedFiles := []string{}
	var uploadErr error

	for _, fileHeader := range files {
		// Validate filename (prevent path traversal)
		filename := filepath.Base(fileHeader.Filename)
		if filename == "" || filename == "." || filename == ".." {
			uploadErr = fmt.Errorf("invalid filename: %s", fileHeader.Filename)
			break
		}

		// Open uploaded file
		file, err := fileHeader.Open()
		if err != nil {
			uploadErr = fmt.Errorf("failed to open uploaded file: %w", err)
			break
		}

		// Create destination file
		destPath := filepath.Join(absTargetDir, filename)
		destFile, err := os.Create(destPath)
		if err != nil {
			file.Close()
			uploadErr = fmt.Errorf("failed to create file: %w", err)
			break
		}

		// Copy file contents
		_, err = io.Copy(destFile, file)
		file.Close()
		destFile.Close()

		if err != nil {
			os.Remove(destPath) // Clean up partial file
			uploadErr = fmt.Errorf("failed to write file: %w", err)
			break
		}

		uploadedFiles = append(uploadedFiles, filename)
	}

	if uploadErr != nil {
		// Clean up successfully uploaded files on error
		for _, filename := range uploadedFiles {
			os.Remove(filepath.Join(absTargetDir, filename))
		}
		http.Error(w, uploadErr.Error(), http.StatusInternalServerError)
		h.logger.Printf("Upload failed: %v", uploadErr)
		return
	}

	// Log upload event
	h.eventStore.Add(events.EventFileUpload, user.Username, getClientIP(r), true,
		fmt.Sprintf("files=%d path=%s", len(uploadedFiles), h.getRelativePath(absTargetDir)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"files":   uploadedFiles,
		"count":   len(uploadedFiles),
	})
}

// Delete removes a file or directory
func (h *FileManagerHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	requestedPath := r.URL.Query().Get("path")
	if requestedPath == "" || requestedPath == "/" {
		http.Error(w, "Cannot delete root directory", http.StatusBadRequest)
		return
	}

	// Validate and get absolute path
	absPath, err := h.validatePath(requestedPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Prevent deleting baseDir
	if absPath == h.baseDir {
		http.Error(w, "Cannot delete base directory", http.StatusBadRequest)
		return
	}

	// Check if path exists
	stat, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File or directory not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to access path", http.StatusInternalServerError)
		}
		return
	}

	// Remove file or directory (recursively if directory)
	err = os.RemoveAll(absPath)
	if err != nil {
		http.Error(w, "Failed to delete", http.StatusInternalServerError)
		h.logger.Printf("Failed to delete %s: %v", absPath, err)
		return
	}

	// Log delete event
	itemType := "file"
	if stat.IsDir() {
		itemType = "directory"
	}
	h.eventStore.Add(events.EventFileDelete, user.Username, getClientIP(r), true,
		fmt.Sprintf("type=%s path=%s", itemType, h.getRelativePath(absPath)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("%s deleted successfully", itemType),
	})
}

// MkDir creates a new directory
func (h *FileManagerHandler) MkDir(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Directory name is required", http.StatusBadRequest)
		return
	}

	// Validate directory name (prevent path traversal)
	dirName := filepath.Base(req.Name)
	if dirName == "" || dirName == "." || dirName == ".." || strings.Contains(dirName, "/") || strings.Contains(dirName, "\\") {
		http.Error(w, "Invalid directory name", http.StatusBadRequest)
		return
	}

	// Validate parent path
	parentPath := req.Path
	if parentPath == "" {
		parentPath = "/"
	}

	absParentDir, err := h.validatePath(parentPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if parent is a directory
	stat, err := os.Stat(absParentDir)
	if err != nil || !stat.IsDir() {
		http.Error(w, "Parent path is not a directory", http.StatusBadRequest)
		return
	}

	// Create new directory
	newDirPath := filepath.Join(absParentDir, dirName)
	err = os.Mkdir(newDirPath, 0755)
	if err != nil {
		if os.IsExist(err) {
			http.Error(w, "Directory already exists", http.StatusConflict)
		} else {
			http.Error(w, "Failed to create directory", http.StatusInternalServerError)
			h.logger.Printf("Failed to create directory %s: %v", newDirPath, err)
		}
		return
	}

	// Log mkdir event
	h.eventStore.Add(events.EventFileMkdir, user.Username, getClientIP(r), true,
		fmt.Sprintf("path=%s", h.getRelativePath(newDirPath)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"path":    h.getRelativePath(newDirPath),
		"name":    dirName,
	})
}

// CreateFile creates a new empty file
func (h *FileManagerHandler) CreateFile(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "File name is required", http.StatusBadRequest)
		return
	}

	// Validate file name (prevent path traversal)
	fileName := filepath.Base(req.Name)
	if fileName == "" || fileName == "." || fileName == ".." || strings.Contains(fileName, "/") || strings.Contains(fileName, "\\") {
		http.Error(w, "Invalid file name", http.StatusBadRequest)
		return
	}

	// Validate parent path
	parentPath := req.Path
	if parentPath == "" {
		parentPath = "/"
	}

	absParentDir, err := h.validatePath(parentPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if parent is a directory
	stat, err := os.Stat(absParentDir)
	if err != nil || !stat.IsDir() {
		http.Error(w, "Parent path is not a directory", http.StatusBadRequest)
		return
	}

	// Create new file path
	newFilePath := filepath.Join(absParentDir, fileName)

	// Check if file already exists
	if _, err := os.Stat(newFilePath); err == nil {
		http.Error(w, "File already exists", http.StatusConflict)
		return
	}

	// Create empty file
	file, err := os.Create(newFilePath)
	if err != nil {
		http.Error(w, "Failed to create file", http.StatusInternalServerError)
		h.logger.Printf("Failed to create file %s: %v", newFilePath, err)
		return
	}
	file.Close()

	// Log file create event
	h.eventStore.Add(events.EventFileWrite, user.Username, getClientIP(r), true,
		fmt.Sprintf("path=%s action=create", h.getRelativePath(newFilePath)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"path":    h.getRelativePath(newFilePath),
		"name":    fileName,
	})
}

// Rename renames a file or directory
func (h *FileManagerHandler) Rename(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	var req struct {
		OldPath string `json:"old_path"`
		NewName string `json:"new_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.OldPath == "" || req.NewName == "" {
		http.Error(w, "Both old_path and new_name are required", http.StatusBadRequest)
		return
	}

	// Validate new name (prevent path traversal)
	newName := filepath.Base(req.NewName)
	if newName == "" || newName == "." || newName == ".." || strings.Contains(newName, "/") || strings.Contains(newName, "\\") {
		http.Error(w, "Invalid new name", http.StatusBadRequest)
		return
	}

	// Validate old path
	absOldPath, err := h.validatePath(req.OldPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Prevent renaming baseDir
	if absOldPath == h.baseDir {
		http.Error(w, "Cannot rename base directory", http.StatusBadRequest)
		return
	}

	// Check if old path exists
	if _, err := os.Stat(absOldPath); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File or directory not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to access path", http.StatusInternalServerError)
		}
		return
	}

	// Build new path (same parent directory)
	parentDir := filepath.Dir(absOldPath)
	absNewPath := filepath.Join(parentDir, newName)

	// Check if new path already exists
	if _, err := os.Stat(absNewPath); err == nil {
		http.Error(w, "A file or directory with that name already exists", http.StatusConflict)
		return
	}

	// Rename
	err = os.Rename(absOldPath, absNewPath)
	if err != nil {
		http.Error(w, "Failed to rename", http.StatusInternalServerError)
		h.logger.Printf("Failed to rename %s to %s: %v", absOldPath, absNewPath, err)
		return
	}

	// Log rename event
	h.eventStore.Add(events.EventFileRename, user.Username, getClientIP(r), true,
		fmt.Sprintf("from=%s to=%s", filepath.Base(absOldPath), newName))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"path":    h.getRelativePath(absNewPath),
		"name":    newName,
	})
}

// ReadFile reads file content for editing (optimized for memory)
func (h *FileManagerHandler) ReadFile(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	requestedPath := r.URL.Query().Get("path")
	if requestedPath == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Validate and get absolute path
	absPath, err := h.validatePath(requestedPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if file exists
	stat, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to access file", http.StatusInternalServerError)
		}
		return
	}

	if stat.IsDir() {
		http.Error(w, "Cannot read directory as file", http.StatusBadRequest)
		return
	}

	// Check file size (limit to 10MB for editing)
	const maxEditSize = 10 * 1024 * 1024
	if stat.Size() > maxEditSize {
		http.Error(w, "File too large to edit (max 10MB)", http.StatusBadRequest)
		return
	}

	// Detect MIME type by extension first (faster)
	ext := strings.ToLower(filepath.Ext(absPath))
	mimeType := getMimeTypeByExtension(ext)

	// Check if file is binary
	isBinary := isBinaryMimeType(mimeType) || isBinaryExtension(ext)

	// For small files (< 1MB) or text files, read fully into memory
	const smallFileThreshold = 1 * 1024 * 1024
	if stat.Size() < smallFileThreshold || !isBinary {
		// Read file content
		content, err := os.ReadFile(absPath)
		if err != nil {
			http.Error(w, "Failed to read file", http.StatusInternalServerError)
			h.logger.Printf("Failed to read file %s: %v", absPath, err)
			return
		}

		// Detect MIME type if not known
		if mimeType == "" {
			mimeType = http.DetectContentType(content)
		}

		// Determine encoding
		encoding := "utf-8"
		contentStr := string(content)

		if isBinary {
			// Encode binary content as base64
			encoding = "base64"
			contentStr = base64.StdEncoding.EncodeToString(content)
		}

		// Log read event
		h.eventStore.Add(events.EventFileRead, user.Username, getClientIP(r), true,
			fmt.Sprintf("file=%s size=%d", filepath.Base(absPath), stat.Size()))

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"content":  contentStr,
			"name":     filepath.Base(absPath),
			"size":     stat.Size(),
			"mimeType": mimeType,
			"encoding": encoding,
			"path":     h.getRelativePath(absPath),
		})
		return
	}

	// For large binary files (>= 1MB), suggest streaming
	// Return metadata only and let client request streaming
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Log read event
	h.eventStore.Add(events.EventFileRead, user.Username, getClientIP(r), true,
		fmt.Sprintf("file=%s size=%d (streaming recommended)", filepath.Base(absPath), stat.Size()))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"content":           "",
		"name":              filepath.Base(absPath),
		"size":              stat.Size(),
		"mimeType":          mimeType,
		"encoding":          "stream", // Signal that streaming should be used
		"streamingRequired": true,
		"path":              h.getRelativePath(absPath),
	})
}

// StreamFile streams file content (optimized for large binary files)
func (h *FileManagerHandler) StreamFile(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	requestedPath := r.URL.Query().Get("path")
	if requestedPath == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Validate and get absolute path
	absPath, err := h.validatePath(requestedPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if file exists
	stat, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to access file", http.StatusInternalServerError)
		}
		return
	}

	if stat.IsDir() {
		http.Error(w, "Cannot stream directory", http.StatusBadRequest)
		return
	}

	// Open file
	file, err := os.Open(absPath)
	if err != nil {
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		h.logger.Printf("Failed to open file %s: %v", absPath, err)
		return
	}
	defer file.Close()

	// Detect content type
	ext := strings.ToLower(filepath.Ext(absPath))
	contentType := getMimeTypeByExtension(ext)
	if contentType == "" {
		contentType = mime.TypeByExtension(ext)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Set headers for inline viewing (not download)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))
	w.Header().Set("Cache-Control", "public, max-age=3600")
	// Support range requests for video/audio seeking
	w.Header().Set("Accept-Ranges", "bytes")

	// Handle range requests (HTTP 206 Partial Content)
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		h.serveFileRange(w, r, file, stat.Size(), contentType)
		return
	}

	// Stream entire file
	_, err = io.Copy(w, file)
	if err != nil {
		h.logger.Printf("Failed to stream file %s: %v", absPath, err)
		return
	}

	// Log stream event
	h.eventStore.Add(events.EventFileRead, user.Username, getClientIP(r), true,
		fmt.Sprintf("stream file=%s size=%d", filepath.Base(absPath), stat.Size()))
}

// serveFileRange handles HTTP range requests for partial content
func (h *FileManagerHandler) serveFileRange(w http.ResponseWriter, r *http.Request, file *os.File, fileSize int64, contentType string) {
	rangeHeader := r.Header.Get("Range")

	// Parse range header (format: "bytes=start-end")
	var start, end int64
	if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); err != nil {
		// Try parsing "bytes=start-" format
		if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-", &start); err != nil {
			http.Error(w, "Invalid range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		end = fileSize - 1
	}

	// Validate range
	if start < 0 || start >= fileSize || end < start || end >= fileSize {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", fileSize))
		http.Error(w, "Invalid range", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	// Seek to start position
	if _, err := file.Seek(start, 0); err != nil {
		http.Error(w, "Failed to seek file", http.StatusInternalServerError)
		return
	}

	// Set headers for partial content
	contentLength := end - start + 1
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", contentLength))
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(http.StatusPartialContent)

	// Stream the requested range
	_, err := io.CopyN(w, file, contentLength)
	if err != nil && err != io.EOF {
		h.logger.Printf("Failed to stream file range: %v", err)
	}
}

// WriteFile saves file content after editing
func (h *FileManagerHandler) WriteFile(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Validate and get absolute path
	absPath, err := h.validatePath(req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if file exists and is not a directory
	stat, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to access file", http.StatusInternalServerError)
		}
		return
	}

	if stat.IsDir() {
		http.Error(w, "Cannot write to directory", http.StatusBadRequest)
		return
	}

	// Write file content
	err = os.WriteFile(absPath, []byte(req.Content), stat.Mode())
	if err != nil {
		http.Error(w, "Failed to write file", http.StatusInternalServerError)
		h.logger.Printf("Failed to write file %s: %v", absPath, err)
		return
	}

	// Log write event
	h.eventStore.Add(events.EventFileWrite, user.Username, getClientIP(r), true,
		fmt.Sprintf("file=%s size=%d", filepath.Base(absPath), len(req.Content)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"name":    filepath.Base(absPath),
		"size":    len(req.Content),
	})
}

// getMimeTypeByExtension returns MIME type for known file extensions
func getMimeTypeByExtension(ext string) string {
	return mimeTypesByExtension[ext]
}

// isBinaryMimeType checks if MIME type represents binary content
func isBinaryMimeType(mimeType string) bool {
	for _, prefix := range binaryMimePrefixes {
		if strings.HasPrefix(mimeType, prefix) {
			return true
		}
	}
	return false
}

// isBinaryExtension checks if file extension represents binary content
func isBinaryExtension(ext string) bool {
	ext = strings.ToLower(ext)
	for _, binaryExt := range binaryExtensions {
		if ext == binaryExt {
			return true
		}
	}
	return false
}

// sanitizeFilename removes dangerous characters from filename to prevent HTTP header injection
func sanitizeFilename(filename string) string {
	// Remove control characters, quotes, and newlines
	sanitized := strings.Map(func(r rune) rune {
		// Allow alphanumeric, common punctuation, spaces, and safe special chars
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == ' ' {
			return r
		}
		// Replace unsafe characters with underscore
		return '_'
	}, filename)

	// Limit length to prevent overly long headers
	if len(sanitized) > 255 {
		sanitized = sanitized[:255]
	}

	return sanitized
}
