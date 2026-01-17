# dir-mimic

A folder synchronization tool that mirrors directory structures using file identification. Includes a web UI for visualizing and applying changes.

## How it works

1. Run `dir-mimic` pointing to your **target directory** (the one you want to modify)
2. Open the web UI in your browser
3. Drag & drop your **source directory** (the structure you want to replicate)
4. Review the planned operations in the tree view
5. Click "Apply Changes" and confirm in the terminal

The tool identifies files by filename + size (optionally with sample hash), then generates move, copy, and delete operations to make the target match the source structure.

## Installation

```bash
go build -o dir-mimic main.go
```

## Usage

```bash
# Basic usage (matches files by name + size)
./dir-mimic /path/to/target

# With sample hash for better matching (-H flag)
./dir-mimic -H /path/to/target

# Custom port
./dir-mimic -p 3000 /path/to/target
```

### Flags

| Flag | Description |
|------|-------------|
| `-H` | Enable sample hash (first+last 64KB) for file identification |
| `-p` | HTTP server port (default: 8080) |

## Operations

The tool generates four types of operations:

| Operation | Description |
|-----------|-------------|
| **Move** | File exists in target but in wrong location |
| **Copy** | File needs to exist in multiple locations |
| **Delete** | File exists in target but not in source |
| **Missing** | File exists in source but not in target (requires external sync) |

Missing files are displayed in the UI but not executed - use `rsync` or similar to copy them from the source.

## Example

```bash
# Source structure (what you want)
/source/
  photos/
    2024/
      vacation.jpg
  docs/
    notes.txt

# Target structure (what you have)
/target/
  photos/
    old/
      vacation.jpg    # Same file, wrong location
      extra.jpg       # Not in source
  docs/

# Run dir-mimic
./dir-mimic /target

# After dropping /source in the UI, you'll see:
#   Move: photos/old/vacation.jpg → photos/2024/
#   Delete: photos/old/extra.jpg
#   Missing: docs/notes.txt
```

## Security

- All operations require terminal confirmation before execution
- Plan checksum (SHA-256) is displayed for verification
- Server only listens on localhost by default

## Browser Support

- **Chrome/Edge**: Full support with File System Access API (click to select folder)
- **Firefox/Safari**: Drag & drop only (using webkitGetAsEntry fallback)
