#!/usr/bin/env python3

import argparse
import hashlib
import json
import os
import sys
from datetime import datetime
from pathlib import Path
from typing import Dict, Any, Optional


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


def mirror_command(args):
    """Execute mirror command (placeholder)"""
    print("Mirror command - placeholder implementation")
    print(f"Target directory: {args.target_dir}")
    print(f"Inventory file: {args.inventory}")
    print(f"Level: {args.level}")
    print(f"Dry run: {args.dry_run}")
    print(f"Delete extra: {args.delete_extra}")
    print(f"Verbose: {args.verbose}")
    
    # TODO: Implement mirror functionality
    print("Mirror functionality not yet implemented")
    return 0


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
    mirror_parser.add_argument('--dry-run', action='store_true',
                             help='Show what would change without modifying files')
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