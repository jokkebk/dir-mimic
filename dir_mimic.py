#!/usr/bin/env python3

import argparse
import hashlib
import json
import os
import shutil
import sys
from datetime import datetime
from pathlib import Path
from typing import Dict, Any, Optional, List, Tuple


class FileRecord:
    def __init__(self, folder: str, filename: str, size: int, sample_sha1: Optional[str] = None, full_sha1: Optional[str] = None):
        self.folder = folder
        self.filename = filename
        self.size = size
        self.sample_sha1 = sample_sha1
        self.full_sha1 = full_sha1
    
    def to_dict(self, level: int) -> Dict[str, Any]:
        result = {"folder": self.folder, "filename": self.filename, "size": self.size}
        if level >= 2 and self.sample_sha1:
            result["sample_sha1"] = self.sample_sha1
        if level >= 3 and self.full_sha1:
            result["full_sha1"] = self.full_sha1
        return result
    
    def get_identity_key(self, level: int) -> Tuple:
        """Return identity key for file matching based on level"""
        if level == 1:
            return (self.filename, self.size)
        elif level == 2:
            return (self.filename, self.size, self.sample_sha1)
        elif level == 3:
            return (self.filename, self.size, self.full_sha1)
        else:
            raise ValueError(f"Invalid level: {level}")
    
    def get_full_path(self) -> str:
        """Return full path combining folder and filename"""
        if self.folder:
            return f"{self.folder}/{self.filename}"
        return self.filename


def calculate_sample_sha1(file_path: Path) -> str:
    """Calculate SHA-1 of first and last 64KB of file"""
    sha1 = hashlib.sha1()
    chunk_size = 64 * 1024  # 64KB
    
    with open(file_path, 'rb') as f:
        # Read first chunk
        first_chunk = f.read(chunk_size)
        sha1.update(first_chunk)
        
        # If file is larger than 64KB, also read last chunk
        file_size = file_path.stat().st_size
        if file_size > chunk_size:
            f.seek(-chunk_size, 2)  # Seek to 64KB from end
            last_chunk = f.read(chunk_size)
            sha1.update(last_chunk)
    
    return sha1.hexdigest()


def calculate_full_sha1(file_path: Path) -> str:
    """Calculate full SHA-1 checksum of file"""
    sha1 = hashlib.sha1()
    
    with open(file_path, 'rb') as f:
        while chunk := f.read(8192):
            sha1.update(chunk)
    
    return sha1.hexdigest()


def scan_directory(source_dir: Path, level: int, verbose: bool) -> list[FileRecord]:
    """Scan directory and return list of FileRecord objects"""
    records = []
    
    if verbose:
        print(f"Scanning {source_dir} with level {level} identification...")
    
    for root, dirs, files in os.walk(source_dir):
        for file in files:
            file_path = Path(root) / file
            relative_path = file_path.relative_to(source_dir)
            
            try:
                size = file_path.stat().st_size
                sample_sha1 = None
                full_sha1 = None
                
                if level >= 2:
                    sample_sha1 = calculate_sample_sha1(file_path)
                    if verbose:
                        print(f"  Sample hash: {relative_path}")
                
                if level >= 3:
                    full_sha1 = calculate_full_sha1(file_path)
                    if verbose:
                        print(f"  Full hash: {relative_path}")
                
                folder_path = str(relative_path.parent) if relative_path.parent != Path('.') else ""
                records.append(FileRecord(folder_path, file, size, sample_sha1, full_sha1))
                
            except (OSError, IOError) as e:
                print(f"Error processing {relative_path}: {e}", file=sys.stderr)
    
    return records


def load_inventory(inventory_file: Path) -> List[FileRecord]:
    """Load inventory from JSONL file"""
    records = []
    try:
        with open(inventory_file, 'r') as f:
            for line_num, line in enumerate(f, 1):
                line = line.strip()
                if not line:
                    continue
                try:
                    data = json.loads(line)
                    record = FileRecord(
                        folder=data.get("folder", ""),
                        filename=data["filename"],
                        size=data["size"],
                        sample_sha1=data.get("sample_sha1"),
                        full_sha1=data.get("full_sha1")
                    )
                    records.append(record)
                except (json.JSONDecodeError, KeyError) as e:
                    print(f"Error parsing line {line_num} in inventory: {e}", file=sys.stderr)
                    continue
    except FileNotFoundError:
        print(f"Error: Inventory file {inventory_file} not found", file=sys.stderr)
        raise
    except IOError as e:
        print(f"Error reading inventory file: {e}", file=sys.stderr)
        raise
    
    return records


def inventory_command(args):
    """Execute inventory command"""
    source_dir = Path(args.source_dir)
    
    if not source_dir.exists():
        print(f"Error: Source directory {source_dir} does not exist", file=sys.stderr)
        return 1
    
    if not source_dir.is_dir():
        print(f"Error: {source_dir} is not a directory", file=sys.stderr)
        return 1
    
    # Generate output filename if not specified
    if args.output:
        output_file = Path(args.output)
    else:
        timestamp = datetime.now().strftime("%Y%m%d-%H%M%S")
        output_file = Path(f"inventory-{timestamp}.jsonl")
    
    if args.verbose:
        print(f"Creating inventory of {source_dir}")
        print(f"Output file: {output_file}")
        print(f"Identification level: {args.level}")
    
    # Scan directory
    records = scan_directory(source_dir, args.level, args.verbose)
    
    # Write JSONL output
    try:
        with open(output_file, 'w') as f:
            for record in records:
                json.dump(record.to_dict(args.level), f, separators=(',', ':'))
                f.write('\n')
        
        if args.verbose:
            print(f"Inventory complete: {len(records)} files processed")
            print(f"Output written to: {output_file}")
        
        return 0
        
    except IOError as e:
        print(f"Error writing output file: {e}", file=sys.stderr)
        return 1


def classify_files(inventory_records: List[FileRecord], target_records: List[FileRecord], 
                  level: int) -> Tuple[Dict, Dict, List[FileRecord], List[FileRecord]]:
    """Classify files as unchanged, moved, or deleted"""
    # Create identity mappings
    inventory_by_identity = {}
    target_by_identity = {}
    
    for record in inventory_records:
        key = record.get_identity_key(level)
        inventory_by_identity[key] = record
    
    for record in target_records:
        key = record.get_identity_key(level)
        target_by_identity[key] = record
    
    unchanged = {}  # identity_key -> (inventory_record, target_record)
    moved = {}      # identity_key -> (inventory_record, target_record)
    missing = []    # inventory records not found in target
    extra = []      # target records not found in inventory
    
    # Find unchanged and moved files
    for identity_key, inv_record in inventory_by_identity.items():
        if identity_key in target_by_identity:
            tgt_record = target_by_identity[identity_key]
            if inv_record.get_full_path() == tgt_record.get_full_path():
                unchanged[identity_key] = (inv_record, tgt_record)
            else:
                moved[identity_key] = (inv_record, tgt_record)
        else:
            missing.append(inv_record)
    
    # Find extra files
    for identity_key, tgt_record in target_by_identity.items():
        if identity_key not in inventory_by_identity:
            extra.append(tgt_record)
    
    return unchanged, moved, missing, extra


def generate_unix_commands(unchanged: Dict, moved: Dict, missing: List[FileRecord], 
                          extra: List[FileRecord], target_dir: Path, verbose: bool, 
                          delete_extra: bool) -> List[str]:
    """Generate Unix commands for dry-run mode"""
    commands = []
    
    # Generate echo commands for unchanged files (only if verbose)
    if verbose:
        for identity_key, (inv_record, tgt_record) in unchanged.items():
            commands.append(f"echo {tgt_record.get_full_path()} unchanged")
    
    # Generate mv commands for moved files
    for identity_key, (inv_record, tgt_record) in moved.items():
        current_path = tgt_record.get_full_path()
        target_path = inv_record.get_full_path()
        
        # Add mkdir -p if target directory doesn't exist
        target_parent = str(Path(target_path).parent)
        if target_parent and target_parent != ".":
            commands.append(f"mkdir -p {target_parent}")
        
        commands.append(f"mv {current_path} {target_path}")
    
    # Generate copy commands for missing files (placeholder - would need source dir)
    for record in missing:
        # This would require access to source directory - for now just comment
        commands.append(f"# TODO: copy from source to {record.get_full_path()}")
    
    # Generate rm commands for extra files
    if delete_extra:
        for record in extra:
            commands.append(f"rm {record.get_full_path()}")
    
    return commands


def execute_file_operations(unchanged: Dict, moved: Dict, missing: List[FileRecord], 
                           extra: List[FileRecord], target_dir: Path, delete_extra: bool, 
                           verbose: bool) -> bool:
    """Execute actual file operations"""
    success = True
    
    # Handle moved files
    for identity_key, (inv_record, tgt_record) in moved.items():
        current_path = target_dir / tgt_record.get_full_path()
        target_path = target_dir / inv_record.get_full_path()
        
        try:
            # Create target directory if needed
            target_path.parent.mkdir(parents=True, exist_ok=True)
            
            # Move file
            shutil.move(str(current_path), str(target_path))
            
            if verbose:
                print(f"Moved: {tgt_record.get_full_path()} -> {inv_record.get_full_path()}")
        
        except (OSError, IOError) as e:
            print(f"Error moving {current_path} to {target_path}: {e}", file=sys.stderr)
            success = False
    
    # Handle missing files (would need source directory - placeholder)
    if missing and verbose:
        print(f"Warning: {len(missing)} files need to be copied from source (not implemented)")
    
    # Handle extra files
    if delete_extra:
        for record in extra:
            file_path = target_dir / record.get_full_path()
            try:
                file_path.unlink()
                if verbose:
                    print(f"Deleted: {record.get_full_path()}")
            except (OSError, IOError) as e:
                print(f"Error deleting {file_path}: {e}", file=sys.stderr)
                success = False
    
    return success


def mirror_command(args):
    """Execute mirror command"""
    target_dir = Path(args.target_dir)
    inventory_file = Path(args.inventory)
    
    # Validate inputs
    if not target_dir.exists():
        print(f"Error: Target directory {target_dir} does not exist", file=sys.stderr)
        return 1
    
    if not target_dir.is_dir():
        print(f"Error: {target_dir} is not a directory", file=sys.stderr)
        return 1
    
    if not inventory_file.exists():
        print(f"Error: Inventory file {inventory_file} does not exist", file=sys.stderr)
        return 1
    
    try:
        # Load inventory
        if args.verbose:
            print(f"Loading inventory from {inventory_file}")
        inventory_records = load_inventory(inventory_file)
        
        if args.verbose:
            print(f"Loaded {len(inventory_records)} files from inventory")
        
        # Scan target directory
        if args.verbose:
            print(f"Scanning target directory {target_dir}")
        target_records = scan_directory(target_dir, args.level, args.verbose)
        
        if args.verbose:
            print(f"Found {len(target_records)} files in target directory")
        
        # Classify files
        unchanged, moved, missing, extra = classify_files(inventory_records, target_records, args.level)
        
        # Print summary
        print(f"File analysis complete:")
        print(f"  Unchanged: {len(unchanged)}")
        print(f"  Moved: {len(moved)}")
        print(f"  Missing from target: {len(missing)}")
        print(f"  Extra in target: {len(extra)}")
        
        if args.doit:
            # Execute actual operations
            if args.verbose:
                print("\nExecuting file operations...")
            
            success = execute_file_operations(unchanged, moved, missing, extra, 
                                            target_dir, args.delete_extra, args.verbose)
            
            if not success:
                print("Some operations failed", file=sys.stderr)
                return 1
            
            print("Mirror operation completed successfully")
        else:
            # Generate and print Unix commands (default dry-run)
            commands = generate_unix_commands(unchanged, moved, missing, extra, 
                                            target_dir, args.verbose, args.delete_extra)
            if commands:
                for command in commands:
                    print(command)
            else:
                if args.verbose:
                    print("No actions needed.")
        
        return 0
        
    except Exception as e:
        print(f"Error during mirror operation: {e}", file=sys.stderr)
        return 1


def main():
    parser = argparse.ArgumentParser(
        prog='dir-mimic',
        description='Record and replicate directory states using file identification'
    )
    
    subparsers = parser.add_subparsers(dest='command', help='Available commands')
    
    # Inventory command
    inventory_parser = subparsers.add_parser('inventory', help='Create inventory of source directory')
    inventory_parser.add_argument('source_dir', help='Source directory to inventory')
    inventory_parser.add_argument('-o', '--output', help='Output inventory file')
    inventory_parser.add_argument('-L', '--level', type=int, choices=[1, 2, 3], default=1,
                                help='Identification level (1=name+size, 2=+sample hash, 3=+full hash)')
    inventory_parser.add_argument('-v', '--verbose', action='store_true',
                                help='Print progress messages')
    
    # Mirror command
    mirror_parser = subparsers.add_parser('mirror', help='Mirror target directory from inventory')
    mirror_parser.add_argument('target_dir', help='Target directory to synchronize')
    mirror_parser.add_argument('--inventory', required=True, help='Inventory file to use')
    mirror_parser.add_argument('-L', '--level', type=int, choices=[1, 2, 3], default=1,
                             help='Identification level (must match inventory)')
    mirror_parser.add_argument('--doit', action='store_true',
                             help='Actually perform file operations (default is dry-run)')
    mirror_parser.add_argument('--delete-extra', action='store_true',
                             help='Delete files in target that are not in inventory')
    mirror_parser.add_argument('-v', '--verbose', action='store_true',
                             help='Print detailed progress messages')
    
    args = parser.parse_args()
    
    if not args.command:
        parser.print_help()
        return 1
    
    if args.command == 'inventory':
        return inventory_command(args)
    elif args.command == 'mirror':
        return mirror_command(args)
    else:
        print(f"Unknown command: {args.command}", file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())