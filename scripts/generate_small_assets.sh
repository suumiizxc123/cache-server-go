#!/bin/bash
# ═══════════════════════════════════════════════════════
#  Generate SMALL assets for Varnish RAM cache stress test
#
#  Goal: Many small files (1KB-100KB) that fit entirely in
#  Varnish 1GB RAM cache → maximum RPS, zero timeouts
#
#  Usage:
#    bash scripts/generate_small_assets.sh [COUNT]
#    bash scripts/generate_small_assets.sh 500
# ═══════════════════════════════════════════════════════

set -e

COUNT=${1:-200}
ASSET_DIR="sample-assets-small"
mkdir -p "$ASSET_DIR"

echo "══════════════════════════════════════"
echo "  Generating $COUNT Small Assets"
echo "  (Varnish RAM cache optimized)"
echo "══════════════════════════════════════"

python3 -c "
import struct, zlib, os, hashlib, random, math

random.seed(42)
count = $COUNT
asset_dir = '$ASSET_DIR'

def make_png_chunk(chunk_type, data):
    chunk = chunk_type + data
    return struct.pack('>I', len(data)) + chunk + struct.pack('>I', zlib.crc32(chunk) & 0xffffffff)

def create_png(width, height, filename, seed):
    ihdr_data = struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0)
    ihdr = make_png_chunk(b'IHDR', ihdr_data)
    raw_data = bytearray()
    for y in range(height):
        raw_data.append(0)
        for x in range(width):
            h = hashlib.md5(f'{seed},{x},{y}'.encode()).digest()
            raw_data.extend([h[0], h[1], h[2]])
    compressed = zlib.compress(bytes(raw_data), 9)
    idat = make_png_chunk(b'IDAT', compressed)
    iend = make_png_chunk(b'IEND', b'')
    with open(filename, 'wb') as f:
        f.write(b'\x89PNG\r\n\x1a\n')
        f.write(ihdr)
        f.write(idat)
        f.write(iend)
    return os.path.getsize(filename)

def create_svg(filename, seed, num_elements):
    lines = ['<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 400 400\" width=\"400\" height=\"400\">']
    lines.append(f'<rect width=\"400\" height=\"400\" fill=\"#{seed*17%256:02x}{seed*31%256:02x}{seed*47%256:02x}\"/>')
    rng = random.Random(seed)
    for i in range(num_elements):
        x, y = rng.randint(0, 380), rng.randint(0, 380)
        r = rng.randint(3, 20)
        cr, cg, cb = rng.randint(0,255), rng.randint(0,255), rng.randint(0,255)
        lines.append(f'<circle cx=\"{x}\" cy=\"{y}\" r=\"{r}\" fill=\"rgb({cr},{cg},{cb})\" opacity=\"0.8\"/>')
    lines.append('</svg>')
    content = '\n'.join(lines)
    with open(filename, 'w') as f:
        f.write(content)
    return os.path.getsize(filename)

def create_css(filename, seed, num_rules):
    rng = random.Random(seed)
    lines = [f'/* Generated CSS #{seed} */']
    for i in range(num_rules):
        r, g, b = rng.randint(0,255), rng.randint(0,255), rng.randint(0,255)
        lines.append(f'.el-{seed}-{i} {{ color: rgb({r},{g},{b}); padding: {rng.randint(1,32)}px; margin: {rng.randint(0,16)}px; display: flex; }}')
    with open(filename, 'w') as f:
        f.write('\n'.join(lines))
    return os.path.getsize(filename)

def create_js(filename, seed, num_funcs):
    rng = random.Random(seed)
    lines = [f'// Generated JS #{seed}']
    for i in range(num_funcs):
        lines.append(f'function fn_{seed}_{i}(x) {{ return x * {rng.random():.6f} + {rng.randint(-100,100)}; }}')
    with open(filename, 'w') as f:
        f.write('\n'.join(lines))
    return os.path.getsize(filename)

def create_binary(filename, size):
    data = hashlib.sha256(filename.encode()).digest()
    with open(filename, 'wb') as f:
        written = 0
        while written < size:
            chunk = data * ((size - written) // len(data) + 1)
            to_write = min(len(chunk), size - written)
            f.write(chunk[:to_write])
            written += to_write
            data = hashlib.sha256(data).digest()
    return os.path.getsize(filename)

total_size = 0
file_count = 0

# Distribution: 40% images, 20% SVG, 15% CSS, 15% JS, 10% binary (fonts/woff)
img_count = int(count * 0.40)
svg_count = int(count * 0.20)
css_count = int(count * 0.15)
js_count = int(count * 0.15)
bin_count = count - img_count - svg_count - css_count - js_count

print(f'  Images: {img_count}, SVGs: {svg_count}, CSS: {css_count}, JS: {js_count}, Binary: {bin_count}')
print()

# PNG images (2KB - 50KB): small thumbnails and icons
print('→ Generating PNG images...')
for i in range(img_count):
    # Vary size: mostly small (32-128px), some medium (128-256px)
    if i < img_count * 0.7:
        w = random.randint(16, 64)
        h = random.randint(16, 64)
    else:
        w = random.randint(64, 128)
        h = random.randint(64, 128)
    name = f'img-{i:04d}.png'
    sz = create_png(w, h, f'{asset_dir}/{name}', i)
    total_size += sz
    file_count += 1
    if i % 20 == 0:
        print(f'  {name} ({w}x{h}) = {sz:,} bytes')

# SVG files (1KB - 20KB)
print('→ Generating SVG files...')
for i in range(svg_count):
    elements = random.randint(10, 200)
    name = f'icon-{i:04d}.svg'
    sz = create_svg(f'{asset_dir}/{name}', i + 1000, elements)
    total_size += sz
    file_count += 1
    if i % 10 == 0:
        print(f'  {name} ({elements} elements) = {sz:,} bytes')

# CSS files (5KB - 50KB)
print('→ Generating CSS files...')
for i in range(css_count):
    rules = random.randint(50, 500)
    name = f'style-{i:04d}.css'
    sz = create_css(f'{asset_dir}/{name}', i + 2000, rules)
    total_size += sz
    file_count += 1
    if i % 10 == 0:
        print(f'  {name} ({rules} rules) = {sz:,} bytes')

# JS files (5KB - 50KB)
print('→ Generating JS files...')
for i in range(js_count):
    funcs = random.randint(50, 500)
    name = f'script-{i:04d}.js'
    sz = create_js(f'{asset_dir}/{name}', i + 3000, funcs)
    total_size += sz
    file_count += 1
    if i % 10 == 0:
        print(f'  {name} ({funcs} funcs) = {sz:,} bytes')

# Binary files: fonts, woff2 (10KB - 100KB)
print('→ Generating binary files...')
for i in range(bin_count):
    size = random.randint(10 * 1024, 100 * 1024)
    name = f'font-{i:04d}.woff2'
    sz = create_binary(f'{asset_dir}/{name}', size)
    total_size += sz
    file_count += 1
    if i % 5 == 0:
        print(f'  {name} = {sz:,} bytes')

print()
print('══════════════════════════════════════')
print(f'  Total: {file_count} files, {total_size:,} bytes ({total_size/1024/1024:.1f} MB)')
print(f'  Avg file size: {total_size//file_count:,} bytes ({total_size//file_count//1024} KB)')
print('══════════════════════════════════════')
"

echo ""
echo "Done! Use with load test:"
echo "  go run ./scripts/asset_loadtest.go -assets $ASSET_DIR -ha=true -host=172.16.22.24 -n 20000 -c 200"
