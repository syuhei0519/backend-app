# Git で追跡しているソースだけを検査用に取り出し、ローカルの秘密や生成物の混入を防ぎます。
"""Read exact HEAD blobs without honoring archive export-ignore/subst attributes."""
from pathlib import PurePosixPath
import re
import subprocess


def tracked_files(root, suffixes=None, revision='HEAD'):
    if revision != 'HEAD' and not re.fullmatch(r'[0-9a-f]{40}', revision):
        raise ValueError('Tracked revision rejected')
    tree = subprocess.run(['git', 'ls-tree', '-rzl', '--full-tree', revision], cwd=root,
                          check=True, capture_output=True, timeout=60).stdout
    selected = []
    total = 0
    for entry in tree.split(b'\0'):
        if not entry:
            continue
        header, encoded_path = entry.split(b'\t', 1)
        path = encoded_path.decode('utf-8')
        if suffixes is not None and PurePosixPath(path).suffix not in suffixes:
            continue
        mode, kind, digest, size = header.split()
        if (mode not in (b'100644', b'100755') or kind != b'blob' or
                not re.fullmatch(rb'[0-9a-f]{40}', digest) or
                not path or path.startswith('/') or
                any(p in ('', '.', '..') for p in path.split('/')) or
                re.search(r'[\x00-\x1f\x7f\\:*?\[\]]', path)):
            raise ValueError('Tracked source type or path rejected')
        length = int(size)
        total += length
        if length > 10 * 1024 * 1024 or total > 100 * 1024 * 1024:
            raise ValueError('Tracked source size rejected')
        selected.append((path, digest, length))
    if not selected:
        raise ValueError('Tracked source is empty')
    data = subprocess.run(['git', 'cat-file', '--batch'], cwd=root,
                          input=b''.join(digest + b'\n' for _, digest, _ in selected),
                          check=True, capture_output=True, timeout=60).stdout
    result, offset = {}, 0
    for path, digest, length in selected:
        line_end = data.index(b'\n', offset)
        if data[offset:line_end] != digest + b' blob ' + str(length).encode():
            raise ValueError('Tracked blob identity mismatch')
        offset = line_end + 1
        if len(data) < offset + length + 1 or data[offset + length:offset + length + 1] != b'\n':
            raise ValueError('Tracked blob incomplete')
        result[path] = data[offset:offset + length]
        offset += length + 1
    if offset != len(data):
        raise ValueError('Tracked blob trailing bytes')
    return result
