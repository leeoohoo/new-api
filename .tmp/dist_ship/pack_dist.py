from pathlib import Path
import hashlib
import tarfile

root = Path('web/dist')
out = Path('.tmp/dist_ship')
out.mkdir(parents=True, exist_ok=True)
manifest = out / 'web_dist_manifest.txt'
hashes = out / 'web_dist_hashes.txt'
tar_path = out / 'web_dist_full.tar.gz'

files = sorted([p for p in root.rglob('*') if p.is_file()])
manifest_lines = []
hash_lines = []
for p in files:
    rel = p.relative_to(root)
    data = p.read_bytes()
    manifest_lines.append(f"{rel}\t{p.stat().st_size}")
    hash_lines.append(f"{hashlib.sha256(data).hexdigest()}  {rel}")

manifest.write_text(('\n'.join(manifest_lines) + '\n') if manifest_lines else '', encoding='utf-8')
hashes.write_text(('\n'.join(hash_lines) + '\n') if hash_lines else '', encoding='utf-8')

if tar_path.exists():
    tar_path.unlink()
with tarfile.open(tar_path, 'w:gz') as tf:
    tf.add(root, arcname='dist')

key_files = [
    Path('web/dist/index.html'),
    Path('web/dist/assets/index-PdjsUHLo.js'),
    Path('web/dist/assets/index-NSpvvuou.css'),
]
print(f'file_count\t{len(files)}')
print(f'tar_size\t{tar_path.stat().st_size}')
for p in key_files:
    data = p.read_bytes()
    print(f'KEY\t{p.as_posix()}\t{p.stat().st_size}\t{hashlib.sha256(data).hexdigest()}')
print(f'TAR_SHA256\t{hashlib.sha256(tar_path.read_bytes()).hexdigest()}')
