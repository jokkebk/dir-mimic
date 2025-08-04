# dir-mimic — Simplified Markdown Specification

## 1. Overview

`dir-mimic` is a two-step command-line tool that records the exact state of a source folder and later makes a target folder match that state.
It works only with files (directory layout is ignored) and relies on three user-selectable identification levels.

## 2. Identification Levels

| Level | Option | How a file is identified | Typical use-case |
|-------|--------|-------------------------|------------------|
| 1 | `-L 1` (default) | Filename + Size | Fast, everyday syncing |
| 2 | `-L 2` | Filename + Size + Checksum of first & last 64 kB (SHA-1) | Detect edits that keep size |
| 3 | `-L 3` | Filename + Full SHA-1 checksum | Byte-accurate archival sync |

## 3. Inventory Command

```
dir-mimic inventory <SOURCE_DIR> [-o <FILE>] [-L {1|2|3}] [-v|--verbose]
```

### Output
- **Default**: inventory-YYYYMMDD-HHMMSS.jsonl in current directory.
- **Format**: JSONL (one JSON object per line).
- **Per-file record** (fields omitted when not required by the chosen -L):

```json
{
  "folder": "Music/Album",
  "filename": "track03.flac",
  "size": 42398712,
  "sample_sha1": "2e7d2c03a950...",     // only for -L 2
  "full_sha1":   "04f8996da763..."      // only for -L 3
}
```

### Exit codes
- 0: success
- \>0: error

### Flags
- `-o, --output`: Explicit manifest filename.
- `-L, --level`: Identification level (1–3).
- `-v, --verbose`: Print progress messages.

## 4. Mirror Command

```
dir-mimic mirror <TARGET_DIR> --inventory <FILE>
                  [-L {1|2|3}] [--dry-run] [--delete-extra] [-v|--verbose]
```

### 4.1 Workflow
1. Load inventory and rescan TARGET_DIR using the same -L.
2. Classify each file in target as:
   - **unchanged** – identical match found in inventory.
   - **moved** – identical content found under a different path.
   - **deleted** – file not present in inventory.
3. Plan actions:
   - Copy new/updated files from source path recorded in inventory.
   - Relocate (rename) moved files when possible; otherwise copy+delete.
   - Delete extras if --delete-extra supplied.
4. Report a summary; if not --dry-run, perform changes.

### 4.2 Key Options

| Flag | Meaning |
|------|---------|
| `--dry-run` | Show what would change without touching files. |
| `--delete-extra` | Remove files that exist in target but not in inventory. |
| `-L, --level` | Same meaning as in inventory; must match the manifest. |
| `-v, --verbose` | Print each planned (or executed) action. |

### 4.3 Safety Notes
- All writes use "copy-then-rename" to avoid partial files on interruption.
- The command never modifies the source directory.
- Confirmation is implicit; supply --dry-run first if unsure.

## 5. Examples

```bash
# 1) Create an inventory (level 2) of ~/Photos
dir-mimic inventory ~/Photos -o photos.inv -L 2 -v

# 2) See what would change on the backup drive
dir-mimic mirror /mnt/Backup/Photos --inventory photos.inv --dry-run -v

# 3) Synchronise for real, deleting stray files using full checksums
dir-mimic mirror /mnt/Backup/Photos --inventory photos.inv \
          --delete-extra -L 3 -v
```

## 6. Out-of-Scope Features (by design)
- No multithreading – single-threaded hashing and copying.
- No timestamps – modification times are ignored.
- No directory operations – only files are considered; directory structure is reconstructed as needed.
- No ignore patterns or filters – the entire tree is processed.
- Single output format – JSONL; no CSV/other formats.
- Basic logging – --verbose is the only logging control.

---

This specification captures the minimal feature set requested, providing a clear foundation for implementing or using the simplified dir-mimic utility.