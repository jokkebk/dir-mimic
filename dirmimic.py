import argparse, sys
import os
import json
import hashlib
import datetime
import shutil
from collections import defaultdict, namedtuple

def calc_key(file_path, level):
    """
    Return a dict with keys for the given identification level:
    Level 1: {"size", "filename"}
    Level 2: {"size", "filename", "sample_sha1"}
    Level 3: {"size", "filename", "full_sha1"}
    """
    size = os.path.getsize(file_path)
    filename = os.path.basename(file_path)
    result = {"filename": filename, "size": size}
    if level == 2:
        h = hashlib.sha1()
        with open(file_path, 'rb') as f:
            if size <= 65536:
                data = f.read()
                h.update(data)
            else:
                first = f.read(65536)
                f.seek(-65536, os.SEEK_END)
                last = f.read(65536)
                h.update(first)
                h.update(last)
        result["sample_sha1"] = h.hexdigest()
    elif level == 3:
        h = hashlib.sha1()
        with open(file_path, 'rb') as f:
            while True:
                chunk = f.read(65536)
                if not chunk:
                    break
                h.update(chunk)
        result["full_sha1"] = h.hexdigest()
    return result

def handle_inventory(args):
    source_dir = os.path.abspath(args.source_dir)
    output = open(args.output, "w") if args.output else sys.stdout
    level = args.level
    for root, dirs, files in os.walk(source_dir):
        rel_folder = os.path.relpath(root, source_dir)
        if rel_folder == ".":
            rel_folder = ""
        for fname in files:
            fpath = os.path.join(root, fname)
            entry = {"folder": rel_folder}
            entry.update(calc_key(fpath, level))
            print(json.dumps(entry), file=output)
            if args.verbose:
                print(f"Inventoried: {os.path.join(rel_folder, fname)}", file=sys.stderr)
    if args.output:
        output.close()

def handle_mirror(args):
    # Handle the 'mirror' subcommand
    # args.target_dir, args.inventory, args.level, args.doit, args.delete_extra, args.verbose are available
    print(f"Mirroring to directory: {args.target_dir} from inventory: {args.inventory}")
    
    # Define a named tuple for file keys
    FileKey = namedtuple('FileKey', ['filename', 'size', 'sample_sha1', 'full_sha1'], defaults=(None, None))
    
    # key -> list(source_dirs)
    source_dirs = defaultdict(list)
    level = -1 # Will be set to the level inferred from the inventory file

    # Read the inventory file
    with open(args.inventory, 'r') as inv_file:
        for line in inv_file:
            entry = json.loads(line.strip())
            
            # Create a FileKey from the entry
            key_fields = {
                'filename': entry['filename'],
                'size': entry['size']
            }
            if 'sample_sha1' in entry:
                key_fields['sample_sha1'] = entry['sample_sha1']
            if 'full_sha1' in entry:
                key_fields['full_sha1'] = entry['full_sha1']
            
            key = FileKey(**key_fields)
            source_dirs[key].append(entry['folder'])
            
            if level == -1:
                if "full_sha1" in entry:
                    level = 3
                elif "sample_sha1" in entry:
                    level = 2
                else:
                    level = 1

    if level == -1:
        print("Error: Unable to determine identification level from inventory file.", file=sys.stderr)
        return 1
    elif args.verbose:
        print(f"Inferred identification level from inventory: {level}", file=sys.stderr)

    destination_dirs = defaultdict(list)

    # Walk the target directory and create keys for existing files
    for root, _, files in os.walk(args.target_dir):
        rel_folder = os.path.relpath(root, args.target_dir)
        if rel_folder == ".":
            rel_folder = ""
        for fname in files:
            fpath = os.path.join(root, fname)
            key_dict = calc_key(fpath, level)
            
            # Create a FileKey from the key_dict
            key_fields = {
                'filename': key_dict['filename'],
                'size': key_dict['size']
            }
            if 'sample_sha1' in key_dict:
                key_fields['sample_sha1'] = key_dict['sample_sha1']
            if 'full_sha1' in key_dict:
                key_fields['full_sha1'] = key_dict['full_sha1']
                
            key = FileKey(**key_fields)
            destination_dirs[key].append(rel_folder)

    # Loop through combined keys from source and destination
    for key in set(source_dirs.keys()).union(destination_dirs.keys()):
        source_folders = source_dirs.get(key, [])
        dest_folders = destination_dirs.get(key, [])

        if not source_folders and dest_folders:
            # Files in destination but not in source - mark for removal
            for folder in dest_folders:
                dest_path = os.path.join(args.target_dir, folder, key.filename)
                print(f"rm '{dest_path}'", file=sys.stderr)
                if args.doit: os.remove(dest_path)
        elif source_folders and not dest_folders:
                src_path = os.path.join(args.target_dir, source_folders[0], key.filename)
                print(f"echo Missing: '{src_path}'", file=sys.stderr)
        elif source_folders and dest_folders:
            one_source = source_folders[0] # Guaranteed to exist

            only_source = [s for s in source_folders if s not in dest_folders]
            only_dest = [d for d in dest_folders if d not in source_folders]

            if not only_source and not only_dest: continue # No need to sync

            # Do mv while we have stuff in only_source and only_dest
            for src, dest in zip(only_source, only_dest):
                # Move from dest to src to match
                src_path = os.path.join(args.target_dir, src, key.filename)
                dest_path = os.path.join(args.target_dir, dest, key.filename)
                print(f"mv '{dest_path}' to '{src_path}'", file=sys.stderr)
                if args.doit: os.rename(dest_path, src_path)
            
            # Remove files in destination that are not in source
            for dest in only_dest[len(only_source):]:
                dest_path = os.path.join(args.target_dir, dest, key.filename)
                print(f"rm '{dest_path}'", file=sys.stderr)
                if args.doit: os.remove(dest_path)
            
            # Copy extra missing files to match source
            for src in only_source[len(only_dest):]:
                src_path = os.path.join(args.target_dir, one_source, key.filename)
                dest_path = os.path.join(args.target_dir, src, key.filename)
                print(f"cp '{src_path}' to '{dest_path}'", file=sys.stderr)
                if args.doit: shutil.copy2(src_path, dest_path)

def main():
    parser = argparse.ArgumentParser(
        prog='dir-mimic',
        description='Record and replicate directory states using file identification'
    )
    
    subparsers = parser.add_subparsers(dest='command', help='Available commands')
    
    # Inventory command
    def default_inventory_filename():
        return datetime.datetime.now().strftime("inventory-%Y%m%d-%H%M%S.jsonl")

    inventory_parser = subparsers.add_parser('inventory', help='Create inventory of source directory')
    inventory_parser.add_argument('source_dir', help='Source directory to inventory')
    inventory_parser.add_argument('-o', '--output', help='Output inventory file',
                                 default=default_inventory_filename())
    inventory_parser.add_argument('-L', '--level', type=int, choices=[1, 2, 3], default=1,
                                help='Identification level (1=name+size, 2=+sample hash, 3=+full hash)')
    inventory_parser.add_argument('-v', '--verbose', action='store_true',
                                help='Print progress messages')
    
    # Mirror command
    mirror_parser = subparsers.add_parser('mirror', help='Mirror target directory from inventory')
    mirror_parser.add_argument('target_dir', help='Target directory to synchronize')
    mirror_parser.add_argument('--inventory', required=True, help='Inventory file to use')
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
        return handle_inventory(args)
    elif args.command == 'mirror':
        return handle_mirror(args)
    else:
        print(f"Unknown command: {args.command}", file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())