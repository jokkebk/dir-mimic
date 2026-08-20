package main

import (
	"bufio"
	"crypto/sha1"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed ui.html
var htmlUI string

// FileEntry represents a file in the catalog
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"`
	Hash   string `json:"hash,omitempty"`
	Folder string `json:"folder,omitempty"` // Derived from path
}

// Operation represents a file operation to perform
type Operation struct {
	Type string `json:"type"` // "mv", "cp", "rm", "missing"
	From string `json:"from"`
	To   string `json:"to,omitempty"`
}

// Plan is just a list of operations
type Plan struct {
	Operations []Operation `json:"operations"`
}

// Default ignore patterns (matched against basename using filepath.Match)
var defaultIgnorePatterns = []string{
	"._*",             // macOS AppleDouble resource forks
	".DS_Store",       // macOS folder metadata
	".Spotlight-V100", // macOS Spotlight index
	".Trashes",        // macOS trash
	".fseventsd",      // macOS FS events
	"Thumbs.db",       // Windows thumbnails
	"desktop.ini",     // Windows folder config
}

var (
	targetDir      string
	useHashing     bool
	catalog        []FileEntry
	ignorePatterns []string
)

// shouldIgnore returns true if the given filename matches any active ignore pattern.
func shouldIgnore(name string) bool {
	for _, pattern := range ignorePatterns {
		matched, err := filepath.Match(pattern, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func main() {
	port := flag.Int("p", 8080, "HTTP server port")
	hashFlag := flag.Bool("H", false, "Enable sample hash computation for file identification")
	localhostOnly := flag.Bool("localhost", false, "Listen only on localhost (for local connections)")
	noDefaultIgnores := flag.Bool("no-default-ignores", false, "Disable built-in ignore patterns")
	extraIgnores := flag.String("ignore", "", "Extra ignore patterns (comma-separated, matched against filename)")
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "Usage: dir-mimic [-H] [-p port] [-localhost] [-no-default-ignores] [-ignore patterns] <directory>\n")
		os.Exit(1)
	}

	targetDir = args[0]
	useHashing = *hashFlag

	// Build ignore patterns
	if !*noDefaultIgnores {
		ignorePatterns = append(ignorePatterns, defaultIgnorePatterns...)
	}
	if *extraIgnores != "" {
		for _, p := range strings.Split(*extraIgnores, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				ignorePatterns = append(ignorePatterns, p)
			}
		}
	}

	// Verify directory exists
	info, err := os.Stat(targetDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: %s is not a directory\n", targetDir)
		os.Exit(1)
	}

	// Make targetDir absolute
	targetDir, err = filepath.Abs(targetDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting absolute path: %v\n", err)
		os.Exit(1)
	}

	// Scan directory
	fmt.Fprintf(os.Stderr, "Scanning directory: %s\n", targetDir)
	catalog, err = scanDirectory(targetDir, useHashing)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning directory: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Found %d files\n", len(catalog))

	// Start HTTP server
	http.HandleFunc("/", handleUI)
	http.HandleFunc("/catalog", handleCatalog)
	http.HandleFunc("/apply", handleApply)

	var addr string
	if *localhostOnly {
		addr = fmt.Sprintf("localhost:%d", *port)
	} else {
		addr = fmt.Sprintf("0.0.0.0:%d", *port)
	}
	for _, url := range serverURLs(*port, *localhostOnly) {
		fmt.Println(url)
	}
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

// serverURLs returns addresses that can be used to access the HTTP server.
func serverURLs(port int, localhostOnly bool) []string {
	portString := strconv.Itoa(port)
	if localhostOnly {
		return []string{"http://localhost:" + portString}
	}

	urls := []string{"http://localhost:" + portString}
	seen := map[string]bool{urls[0]: true}

	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		url := "http://" + net.JoinHostPort(hostname, portString)
		urls = append(urls, url)
		seen[url] = true
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return urls
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			var ip net.IP
			switch address := address.(type) {
			case *net.IPNet:
				ip = address.IP
			case *net.IPAddr:
				ip = address.IP
			}
			if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
				continue
			}

			host := ip.String()
			if ip.IsLinkLocalUnicast() && ip.To4() == nil {
				// Link-local IPv6 addresses require the interface scope in URLs.
				host += "%25" + iface.Name
			}
			url := "http://" + net.JoinHostPort(host, portString)
			if !seen[url] {
				urls = append(urls, url)
				seen[url] = true
			}
		}
	}

	return urls
}

// scanDirectory walks the directory and builds the catalog
func scanDirectory(root string, withHash bool) ([]FileEntry, error) {
	var entries []FileEntry

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if shouldIgnore(info.Name()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		entry := FileEntry{
			Path:  relPath,
			Size:  info.Size(),
			MTime: info.ModTime().UnixMilli(),
		}

		if withHash {
			hash, err := computeSampleHash(path, info.Size())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not hash %s: %v\n", relPath, err)
			} else {
				entry.Hash = hash
			}
		}

		entries = append(entries, entry)
		return nil
	})

	return entries, err
}

// computeSampleHash computes a sample SHA1 hash (first+last 64KB)
func computeSampleHash(path string, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha1.New()

	if size <= 65536 {
		_, err = io.Copy(h, f)
	} else {
		// Read first 64KB
		buf := make([]byte, 65536)
		n, err := f.Read(buf)
		if err != nil {
			return "", err
		}
		h.Write(buf[:n])

		// Read last 64KB
		_, err = f.Seek(-65536, io.SeekEnd)
		if err != nil {
			return "", err
		}
		n, err = f.Read(buf)
		if err != nil {
			return "", err
		}
		h.Write(buf[:n])
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// setCORSHeaders adds CORS headers to allow requests from file:// and other origins
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// handleUI serves the embedded HTML UI
func handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(htmlUI))
}

// CatalogResponse contains the catalog plus metadata
type CatalogResponse struct {
	Path           string      `json:"path"`
	Files          []FileEntry `json:"files"`
	FileCount      int         `json:"fileCount"`
	FolderCount    int         `json:"folderCount"`
	TotalSize      int64       `json:"totalSize"`
	IgnorePatterns []string    `json:"ignorePatterns"`
}

// handleCatalog returns the server-side catalog as JSON
func handleCatalog(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		return
	}

	// Calculate stats
	folders := make(map[string]bool)
	var totalSize int64
	for _, entry := range catalog {
		totalSize += entry.Size
		// Extract folder path
		dir := filepath.Dir(entry.Path)
		if dir != "." {
			folders[dir] = true
			// Also count parent folders
			for dir != "." && dir != "/" {
				folders[dir] = true
				dir = filepath.Dir(dir)
			}
		}
	}

	response := CatalogResponse{
		Path:           targetDir,
		Files:          catalog,
		FileCount:      len(catalog),
		FolderCount:    len(folders),
		TotalSize:      totalSize,
		IgnorePatterns: ignorePatterns,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleApply receives a plan and executes it after terminal confirmation
func handleApply(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read raw body for checksum
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var plan Plan
	if err := json.Unmarshal(body, &plan); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Compute checksum of received payload
	checksum := sha256.Sum256(body)
	checksumHex := hex.EncodeToString(checksum[:])

	// Display plan in terminal
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("PLAN TO EXECUTE")
	fmt.Println(strings.Repeat("=", 60))

	mvCount, cpCount, rmCount, missingCount := 0, 0, 0, 0
	for _, op := range plan.Operations {
		switch op.Type {
		case "mv":
			fmt.Printf("  MOVE: %s -> %s\n", op.From, op.To)
			mvCount++
		case "cp":
			fmt.Printf("  COPY: %s -> %s\n", op.From, op.To)
			cpCount++
		case "rm":
			fmt.Printf("  DELETE: %s\n", op.From)
			rmCount++
		case "missing":
			missingCount++
		}
	}

	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("Summary: %d moves, %d copies, %d deletes, %d missing\n", mvCount, cpCount, rmCount, missingCount)
	fmt.Printf("Checksum: %s\n", checksumHex)
	fmt.Println(strings.Repeat("-", 60))

	// Ask for confirmation
	fmt.Print("Execute this plan? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response != "y" && response != "yes" {
		fmt.Println("Aborted.")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "aborted"})
		return
	}

	// Execute operations
	fmt.Println("\nExecuting...")
	errors := []string{}

	for _, op := range plan.Operations {
		var err error
		switch op.Type {
		case "mv":
			err = executeMove(op.From, op.To)
		case "cp":
			err = executeCopy(op.From, op.To)
		case "rm":
			err = executeDelete(op.From)
		case "missing":
			// Nothing to do for missing files
			continue
		}
		if err != nil {
			errMsg := fmt.Sprintf("%s %s: %v", op.Type, op.From, err)
			fmt.Fprintf(os.Stderr, "  ERROR: %s\n", errMsg)
			errors = append(errors, errMsg)
		} else {
			fmt.Printf("  OK: %s %s\n", op.Type, op.From)
		}
	}

	fmt.Println("\nDone!")

	// Rescan directory
	fmt.Fprintf(os.Stderr, "Rescanning directory...\n")
	newCatalog, err := scanDirectory(targetDir, useHashing)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not rescan: %v\n", err)
	} else {
		catalog = newCatalog
	}

	w.Header().Set("Content-Type", "application/json")
	result := map[string]interface{}{
		"status": "completed",
		"errors": errors,
	}
	json.NewEncoder(w).Encode(result)
}

func executeMove(from, to string) error {
	fromPath := filepath.Join(targetDir, from)
	toPath := filepath.Join(targetDir, to)

	// Ensure destination directory exists
	toDir := filepath.Dir(toPath)
	if err := os.MkdirAll(toDir, 0755); err != nil {
		return err
	}

	return os.Rename(fromPath, toPath)
}

func executeCopy(from, to string) error {
	fromPath := filepath.Join(targetDir, from)
	toPath := filepath.Join(targetDir, to)

	// Ensure destination directory exists
	toDir := filepath.Dir(toPath)
	if err := os.MkdirAll(toDir, 0755); err != nil {
		return err
	}

	src, err := os.Open(fromPath)
	if err != nil {
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}

	dst, err := os.Create(toPath)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}

	if err := os.Chmod(toPath, info.Mode()); err != nil {
		return err
	}

	// Preserve the source modification time. Access times are not portable and
	// may be changed by reading the source, so leave the destination access time
	// as the time of the copy.
	if err := os.Chtimes(toPath, time.Now(), info.ModTime()); err != nil {
		return err
	}

	return nil
}

func executeDelete(path string) error {
	fullPath := filepath.Join(targetDir, path)
	return os.Remove(fullPath)
}
