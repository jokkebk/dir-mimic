package main

import (
	"bufio"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

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

// Plan represents the operations to apply
type Plan struct {
	Operations []Operation `json:"operations"`
	Checksum   string      `json:"checksum"`
}

var (
	targetDir  string
	useHashing bool
	catalog    []FileEntry
)

func main() {
	port := flag.Int("p", 8080, "HTTP server port")
	hashFlag := flag.Bool("H", false, "Enable sample hash computation for file identification")
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "Usage: dir-mimic [-H] [-p port] <directory>\n")
		os.Exit(1)
	}

	targetDir = args[0]
	useHashing = *hashFlag

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

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("http://localhost%s\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

// scanDirectory walks the directory and builds the catalog
func scanDirectory(root string, withHash bool) ([]FileEntry, error) {
	var entries []FileEntry

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
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

// handleUI serves the embedded HTML UI
func handleUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(htmlUI))
}

// handleCatalog returns the server-side catalog as JSON
func handleCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(catalog)
}

// handleApply receives a plan and executes it after terminal confirmation
func handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var plan Plan
	if err := json.NewDecoder(r.Body).Decode(&plan); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Verify checksum
	opsJSON, _ := json.Marshal(plan.Operations)
	computed := sha256.Sum256(opsJSON)
	computedHex := hex.EncodeToString(computed[:])

	if plan.Checksum != computedHex {
		http.Error(w, "Checksum mismatch", http.StatusBadRequest)
		return
	}

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
	fmt.Printf("Checksum: %s\n", plan.Checksum[:16]+"...")
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

	dst, err := os.Create(toPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		return err
	}

	// Copy file mode
	info, err := os.Stat(fromPath)
	if err == nil {
		os.Chmod(toPath, info.Mode())
	}

	return nil
}

func executeDelete(path string) error {
	fullPath := filepath.Join(targetDir, path)
	return os.Remove(fullPath)
}

// Embedded HTML UI
const htmlUI = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>dir-mimic</title>
<style>
* {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}

body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  background: #1a1a2e;
  color: #eee;
  min-height: 100vh;
  padding: 20px;
}

.container {
  max-width: 900px;
  margin: 0 auto;
}

header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
  padding-bottom: 15px;
  border-bottom: 1px solid #333;
}

h1 {
  font-size: 1.5rem;
  font-weight: 500;
  color: #fff;
}

.btn {
  background: #4a9eff;
  color: white;
  border: none;
  padding: 10px 20px;
  border-radius: 6px;
  font-size: 0.9rem;
  cursor: pointer;
  transition: background 0.2s;
}

.btn:hover {
  background: #3a8eef;
}

.btn:disabled {
  background: #555;
  cursor: not-allowed;
}

.dropzone {
  border: 2px dashed #444;
  border-radius: 12px;
  padding: 60px 20px;
  text-align: center;
  transition: all 0.2s;
  margin-bottom: 20px;
  cursor: pointer;
}

.dropzone:hover, .dropzone.dragover {
  border-color: #4a9eff;
  background: rgba(74, 158, 255, 0.1);
}

.dropzone-text {
  color: #888;
  font-size: 1rem;
}

.dropzone-text strong {
  color: #ccc;
}

.scanning {
  color: #4a9eff;
}

.tree {
  background: #252540;
  border-radius: 8px;
  padding: 15px;
  margin-bottom: 15px;
  max-height: 500px;
  overflow-y: auto;
}

.tree-node {
  margin: 2px 0;
}

.tree-folder {
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  display: flex;
  align-items: center;
  gap: 8px;
}

.tree-folder:hover {
  background: rgba(255, 255, 255, 0.05);
}

.tree-folder-icon {
  transition: transform 0.2s;
}

.tree-folder.collapsed .tree-folder-icon {
  transform: rotate(-90deg);
}

.tree-children {
  margin-left: 20px;
  border-left: 1px solid #333;
  padding-left: 10px;
}

.tree-children.hidden {
  display: none;
}

.tree-file {
  padding: 3px 8px;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.9rem;
}

.op-mv { color: #6eb5ff; }
.op-mv::before { content: "↔️ "; }
.op-cp { color: #6eff9e; }
.op-cp::before { content: "📋 "; }
.op-rm { color: #ff6e6e; }
.op-rm::before { content: "🗑️ "; }
.op-missing { color: #888; }
.op-missing::before { content: "➕ "; }

.folder-stats {
  font-size: 0.8rem;
  color: #666;
  margin-left: auto;
}

.summary {
  background: #252540;
  border-radius: 8px;
  padding: 12px 15px;
  font-size: 0.9rem;
  color: #aaa;
}

.summary span {
  margin-right: 15px;
}

.summary .mv { color: #6eb5ff; }
.summary .cp { color: #6eff9e; }
.summary .rm { color: #ff6e6e; }
.summary .missing { color: #888; }

.status {
  padding: 15px;
  border-radius: 8px;
  margin: 15px 0;
  text-align: center;
}

.status.success {
  background: rgba(110, 255, 158, 0.1);
  color: #6eff9e;
}

.status.error {
  background: rgba(255, 110, 110, 0.1);
  color: #ff6e6e;
}

.status.pending {
  background: rgba(74, 158, 255, 0.1);
  color: #4a9eff;
}

.empty-state {
  text-align: center;
  padding: 40px;
  color: #666;
}
</style>
</head>
<body>
<div class="container">
  <header>
    <h1>dir-mimic</h1>
    <button class="btn" id="applyBtn" disabled>Apply Changes</button>
  </header>

  <div class="dropzone" id="dropzone">
    <div class="dropzone-text" id="dropzoneText">
      <strong>Drag & drop your source folder here</strong><br>
      or click to select
    </div>
  </div>
  <input type="file" id="folderInput" webkitdirectory multiple style="display: none;">

  <div id="content">
    <div class="empty-state">
      Drop a folder above to compare with the server directory
    </div>
  </div>

  <div class="summary" id="summary" style="display: none;">
    <span class="mv">0 moves</span>
    <span class="cp">0 copies</span>
    <span class="rm">0 deletes</span>
    <span class="missing">0 missing files</span>
  </div>
</div>

<script>
// State
let serverCatalog = [];
let sourceCatalog = [];
let operations = [];

// DOM elements
const dropzone = document.getElementById('dropzone');
const dropzoneText = document.getElementById('dropzoneText');
const content = document.getElementById('content');
const summary = document.getElementById('summary');
const applyBtn = document.getElementById('applyBtn');

// Fetch server catalog on load
async function init() {
  try {
    const res = await fetch('/catalog');
    serverCatalog = await res.json();
    console.log('Server catalog loaded:', serverCatalog.length, 'files');
  } catch (err) {
    console.error('Failed to load catalog:', err);
    content.innerHTML = '<div class="status error">Failed to load server catalog</div>';
  }
}

init();

// Drag & drop handling
dropzone.addEventListener('dragover', (e) => {
  e.preventDefault();
  dropzone.classList.add('dragover');
});

dropzone.addEventListener('dragleave', () => {
  dropzone.classList.remove('dragover');
});

dropzone.addEventListener('drop', async (e) => {
  e.preventDefault();
  dropzone.classList.remove('dragover');

  const items = e.dataTransfer.items;
  if (!items || items.length === 0) return;

  // Try File System Access API first (Chrome)
  if (items[0].getAsFileSystemHandle) {
    try {
      const handle = await items[0].getAsFileSystemHandle();
      if (handle.kind === 'directory') {
        await scanDirectoryHandle(handle);
        return;
      }
    } catch (err) {
      console.log('File System Access API failed, trying fallback');
    }
  }

  // Fallback: webkitGetAsEntry
  const item = items[0];
  if (item.webkitGetAsEntry) {
    const entry = item.webkitGetAsEntry();
    if (entry && entry.isDirectory) {
      await scanWebkitEntry(entry);
      return;
    }
  }

  dropzoneText.innerHTML = '<span style="color: #ff6e6e;">Please drop a folder, not a file</span>';
});

const folderInput = document.getElementById('folderInput');

dropzone.addEventListener('click', async () => {
  // Try File System Access API for directory picker (requires secure context)
  if ('showDirectoryPicker' in window && window.isSecureContext) {
    try {
      const handle = await window.showDirectoryPicker();
      await scanDirectoryHandle(handle);
    } catch (err) {
      if (err.name !== 'AbortError') {
        console.error('Directory picker failed:', err);
      }
    }
  } else {
    // Fallback: use hidden file input with webkitdirectory
    folderInput.click();
  }
});

// Handle folder selection via file input (works in non-secure contexts)
folderInput.addEventListener('change', async (e) => {
  const files = e.target.files;
  if (!files || files.length === 0) return;

  dropzoneText.innerHTML = '<span class="scanning">Scanning folder...</span>';
  sourceCatalog = [];

  // Extract folder name from first file's path
  let folderName = '';
  if (files[0].webkitRelativePath) {
    folderName = files[0].webkitRelativePath.split('/')[0];
  }

  for (const file of files) {
    // webkitRelativePath gives us "folder/subfolder/file.txt"
    // We want to strip the root folder name
    let path = file.webkitRelativePath;
    if (path.startsWith(folderName + '/')) {
      path = path.substring(folderName.length + 1);
    }

    sourceCatalog.push({
      path: path,
      size: file.size,
      mtime: file.lastModified
    });
  }

  console.log('Source catalog:', sourceCatalog.length, 'files');
  dropzoneText.innerHTML = '<strong>' + folderName + '</strong><br>' + sourceCatalog.length + ' files scanned';
  computeDiff();

  // Reset input so same folder can be selected again
  folderInput.value = '';
});

// Scan directory using File System Access API
async function scanDirectoryHandle(dirHandle, basePath = '') {
  dropzoneText.innerHTML = '<span class="scanning">Scanning folder...</span>';
  sourceCatalog = [];

  async function walkDir(handle, path) {
    for await (const entry of handle.values()) {
      const entryPath = path ? path + '/' + entry.name : entry.name;
      if (entry.kind === 'directory') {
        await walkDir(entry, entryPath);
      } else {
        try {
          const file = await entry.getFile();
          sourceCatalog.push({
            path: entryPath,
            size: file.size,
            mtime: file.lastModified
          });
        } catch (err) {
          console.warn('Could not read file:', entryPath, err);
        }
      }
    }
  }

  await walkDir(dirHandle, '');
  console.log('Source catalog:', sourceCatalog.length, 'files');
  dropzoneText.innerHTML = '<strong>' + dirHandle.name + '</strong><br>' + sourceCatalog.length + ' files scanned';
  computeDiff();
}

// Scan directory using webkit fallback
async function scanWebkitEntry(entry) {
  dropzoneText.innerHTML = '<span class="scanning">Scanning folder...</span>';
  sourceCatalog = [];

  function readEntries(dirReader) {
    return new Promise((resolve) => {
      dirReader.readEntries(resolve);
    });
  }

  function readFile(fileEntry) {
    return new Promise((resolve, reject) => {
      fileEntry.file(resolve, reject);
    });
  }

  async function walkEntry(entry, path) {
    if (entry.isFile) {
      try {
        const file = await readFile(entry);
        sourceCatalog.push({
          path: path,
          size: file.size,
          mtime: file.lastModified
        });
      } catch (err) {
        console.warn('Could not read file:', path, err);
      }
    } else if (entry.isDirectory) {
      const reader = entry.createReader();
      let entries;
      do {
        entries = await readEntries(reader);
        for (const child of entries) {
          const childPath = path ? path + '/' + child.name : child.name;
          await walkEntry(child, childPath);
        }
      } while (entries.length > 0);
    }
  }

  await walkEntry(entry, '');
  console.log('Source catalog:', sourceCatalog.length, 'files');
  dropzoneText.innerHTML = '<strong>' + entry.name + '</strong><br>' + sourceCatalog.length + ' files scanned';
  computeDiff();
}

// Compute diff between source and server catalogs
function computeDiff() {
  operations = [];

  // Build key maps: key = filename + '|' + size
  function makeKey(entry) {
    const filename = entry.path.split('/').pop();
    return filename + '|' + entry.size + (entry.hash ? '|' + entry.hash : '');
  }

  function getFolder(path) {
    const parts = path.split('/');
    parts.pop();
    return parts.join('/');
  }

  // Map: key -> [folders]
  const sourceFolders = new Map();
  const destFolders = new Map();

  for (const entry of sourceCatalog) {
    const key = makeKey(entry);
    if (!sourceFolders.has(key)) sourceFolders.set(key, []);
    sourceFolders.get(key).push({folder: getFolder(entry.path), path: entry.path, size: entry.size});
  }

  for (const entry of serverCatalog) {
    const key = makeKey(entry);
    if (!destFolders.has(key)) destFolders.set(key, []);
    destFolders.get(key).push({folder: getFolder(entry.path), path: entry.path});
  }

  // Get all unique keys
  const allKeys = new Set([...sourceFolders.keys(), ...destFolders.keys()]);

  for (const key of allKeys) {
    const srcFolderList = sourceFolders.get(key) || [];
    const dstFolderList = destFolders.get(key) || [];

    const srcFolderSet = new Set(srcFolderList.map(f => f.folder));
    const dstFolderSet = new Set(dstFolderList.map(f => f.folder));

    if (srcFolderList.length === 0 && dstFolderList.length > 0) {
      // Only in destination - delete
      for (const dst of dstFolderList) {
        operations.push({type: 'rm', from: dst.path});
      }
    } else if (srcFolderList.length > 0 && dstFolderList.length === 0) {
      // Only in source - missing
      for (const src of srcFolderList) {
        operations.push({type: 'missing', from: src.path, size: src.size});
      }
    } else {
      // In both - compare folders
      const onlyInSrc = srcFolderList.filter(s => !dstFolderSet.has(s.folder));
      const onlyInDst = dstFolderList.filter(d => !srcFolderSet.has(d.folder));

      if (onlyInSrc.length === 0 && onlyInDst.length === 0) continue;

      // Move where possible
      const moveCount = Math.min(onlyInSrc.length, onlyInDst.length);
      for (let i = 0; i < moveCount; i++) {
        operations.push({
          type: 'mv',
          from: onlyInDst[i].path,
          to: onlyInSrc[i].path
        });
      }

      // Delete extra files in destination
      for (let i = moveCount; i < onlyInDst.length; i++) {
        operations.push({type: 'rm', from: onlyInDst[i].path});
      }

      // Copy for extra files needed in source locations
      if (onlyInSrc.length > moveCount && dstFolderList.length > 0) {
        for (let i = moveCount; i < onlyInSrc.length; i++) {
          operations.push({
            type: 'cp',
            from: dstFolderList[0].path,
            to: onlyInSrc[i].path
          });
        }
      }
    }
  }

  // Sort operations
  operations.sort((a, b) => a.from.localeCompare(b.from));

  renderTree();
  updateSummary();
}

// Build tree structure from operations
function buildTree(ops) {
  const root = {name: '', children: new Map(), ops: []};

  for (const op of ops) {
    const path = op.from;
    const parts = path.split('/');
    let node = root;

    // Navigate/create path to parent folder
    for (let i = 0; i < parts.length - 1; i++) {
      const part = parts[i];
      if (!node.children.has(part)) {
        node.children.set(part, {name: part, children: new Map(), ops: []});
      }
      node = node.children.get(part);
    }

    node.ops.push({...op, filename: parts[parts.length - 1]});
  }

  return root;
}

// Count operations in a subtree
function countOps(node) {
  const counts = {mv: 0, cp: 0, rm: 0, missing: 0, missingSize: 0};

  for (const op of node.ops) {
    counts[op.type]++;
    if (op.type === 'missing' && op.size) {
      counts.missingSize += op.size;
    }
  }

  for (const child of node.children.values()) {
    const childCounts = countOps(child);
    counts.mv += childCounts.mv;
    counts.cp += childCounts.cp;
    counts.rm += childCounts.rm;
    counts.missing += childCounts.missing;
    counts.missingSize += childCounts.missingSize;
  }

  return counts;
}

// Format file size
function formatSize(bytes) {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
  return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
}

// Render tree to HTML
function renderTree() {
  const tree = buildTree(operations);

  if (operations.length === 0) {
    content.innerHTML = '<div class="empty-state">No differences found - directories are in sync!</div>';
    applyBtn.disabled = true;
    return;
  }

  function renderNode(node, isRoot = false) {
    let html = '';

    // Sort children by name
    const sortedChildren = [...node.children.entries()].sort((a, b) => a[0].localeCompare(b[0]));

    for (const [name, child] of sortedChildren) {
      const counts = countOps(child);
      const hasOps = counts.mv + counts.cp + counts.rm + counts.missing > 0;
      if (!hasOps) continue;

      const statsArr = [];
      if (counts.mv) statsArr.push(counts.mv + ' move' + (counts.mv > 1 ? 's' : ''));
      if (counts.cp) statsArr.push(counts.cp + ' cop' + (counts.cp > 1 ? 'ies' : 'y'));
      if (counts.rm) statsArr.push(counts.rm + ' delete' + (counts.rm > 1 ? 's' : ''));
      if (counts.missing) statsArr.push('+' + counts.missing + ' file' + (counts.missing > 1 ? 's' : '') +
        (counts.missingSize > 0 ? ' (' + formatSize(counts.missingSize) + ')' : ''));

      const id = 'node-' + Math.random().toString(36).substr(2, 9);

      html += '<div class="tree-node">';
      html += '<div class="tree-folder" onclick="toggleFolder(\'' + id + '\', this)">';
      html += '<span class="tree-folder-icon">&#9660;</span>';
      html += '<span>&#128193; ' + name + '</span>';
      html += '<span class="folder-stats">' + statsArr.join(', ') + '</span>';
      html += '</div>';
      html += '<div class="tree-children" id="' + id + '">';
      html += renderNode(child);
      html += '</div>';
      html += '</div>';
    }

    // Sort and render operations
    const sortedOps = [...node.ops].sort((a, b) => a.filename.localeCompare(b.filename));
    for (const op of sortedOps) {
      html += '<div class="tree-file op-' + op.type + '">';
      if (op.type === 'mv') {
        html += op.filename + ' &#8594; ' + getFolder(op.to) + '/';
      } else if (op.type === 'cp') {
        html += op.filename + ' (copy to ' + getFolder(op.to) + '/)';
      } else if (op.type === 'rm') {
        html += op.filename;
      } else if (op.type === 'missing') {
        html += op.filename + (op.size ? ' (' + formatSize(op.size) + ')' : '');
      }
      html += '</div>';
    }

    return html;
  }

  function getFolder(path) {
    if (!path) return '';
    const parts = path.split('/');
    parts.pop();
    return parts.join('/') || '.';
  }

  content.innerHTML = '<div class="tree">' + renderNode(tree, true) + '</div>';
  applyBtn.disabled = false;
}

// Toggle folder collapse
window.toggleFolder = function(id, elem) {
  const children = document.getElementById(id);
  if (children) {
    children.classList.toggle('hidden');
    elem.classList.toggle('collapsed');
  }
};

// Update summary bar
function updateSummary() {
  const counts = {mv: 0, cp: 0, rm: 0, missing: 0, missingSize: 0};
  for (const op of operations) {
    counts[op.type]++;
    if (op.type === 'missing' && op.size) {
      counts.missingSize += op.size;
    }
  }

  summary.style.display = 'block';
  summary.innerHTML =
    '<span class="mv">' + counts.mv + ' move' + (counts.mv !== 1 ? 's' : '') + '</span>' +
    '<span class="cp">' + counts.cp + ' cop' + (counts.cp !== 1 ? 'ies' : 'y') + '</span>' +
    '<span class="rm">' + counts.rm + ' delete' + (counts.rm !== 1 ? 's' : '') + '</span>' +
    '<span class="missing">' + counts.missing + ' missing' +
      (counts.missingSize > 0 ? ' (' + formatSize(counts.missingSize) + ')' : '') + '</span>';
}

// Apply changes
applyBtn.addEventListener('click', async () => {
  // Filter out missing operations (nothing to do on server for those)
  const executableOps = operations.filter(op => op.type !== 'missing');

  if (executableOps.length === 0) {
    alert('No executable operations. Missing files need to be copied from source using rsync or similar.');
    return;
  }

  // Compute checksum
  const opsJson = JSON.stringify(executableOps);
  const hashBuffer = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(opsJson));
  const hashArray = Array.from(new Uint8Array(hashBuffer));
  const checksum = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');

  applyBtn.disabled = true;
  applyBtn.textContent = 'Waiting for confirmation...';
  content.innerHTML = '<div class="status pending">Check the terminal for the plan and confirm execution.</div>';

  try {
    const res = await fetch('/apply', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({operations: executableOps, checksum: checksum})
    });

    const result = await res.json();

    if (result.status === 'completed') {
      if (result.errors && result.errors.length > 0) {
        content.innerHTML = '<div class="status error">Completed with ' + result.errors.length + ' error(s)</div>';
      } else {
        content.innerHTML = '<div class="status success">All operations completed successfully!</div>';
      }
      // Reload catalog
      const catalogRes = await fetch('/catalog');
      serverCatalog = await catalogRes.json();
      operations = [];
      summary.style.display = 'none';
    } else {
      content.innerHTML = '<div class="status error">Plan was aborted in the terminal.</div>';
    }
  } catch (err) {
    content.innerHTML = '<div class="status error">Error: ' + err.message + '</div>';
  }

  applyBtn.textContent = 'Apply Changes';
  applyBtn.disabled = true;
});
</script>
</body>
</html>
`
