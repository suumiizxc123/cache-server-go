#!/bin/bash
# ═══════════════════════════════════════════════════════
#  Generate Real Test Assets — Various sizes & types
#
#  Creates realistic binary content for cache testing:
#    - PNG images (small → large)
#    - JPEG photos (compressed)
#    - SVG vector graphics
#    - WebP images
#    - Fake video files (MP4-sized binary)
#    - CSS/JS bundles
#    - Font files
# ═══════════════════════════════════════════════════════

set -e

ASSET_DIR="sample-assets"
mkdir -p "$ASSET_DIR"

echo "══════════════════════════════════════"
echo "  Generating Real Test Assets"
echo "══════════════════════════════════════"

# ── 1. SVG Files (various complexity) ──
echo "→ Generating SVG files..."

# Simple icon SVG (~2KB)
cat > "$ASSET_DIR/icon-small.svg" << 'SVG'
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="100" height="100">
  <defs>
    <linearGradient id="g1" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" style="stop-color:#667eea"/>
      <stop offset="100%" style="stop-color:#764ba2"/>
    </linearGradient>
  </defs>
  <circle cx="50" cy="50" r="45" fill="url(#g1)" stroke="#333" stroke-width="2"/>
  <text x="50" y="58" text-anchor="middle" fill="white" font-size="24" font-family="Arial">C</text>
</svg>
SVG

# Complex dashboard SVG (~50KB)
{
  echo '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 800" width="1200" height="800">'
  echo '<rect width="1200" height="800" fill="#1a1a2e"/>'
  # Generate 200 data points for a chart
  for i in $(seq 1 1000); do
    x=$((i * 6))
    y=$((RANDOM % 400 + 200))
    r=$((RANDOM % 8 + 2))
    g=$((RANDOM % 200 + 55))
    b=$((RANDOM % 200 + 55))
    echo "<circle cx=\"$x\" cy=\"$y\" r=\"$r\" fill=\"rgb($g,100,$b)\" opacity=\"0.7\"/>"
  done
  # Grid lines
  for i in $(seq 100 100 700); do
    echo "<line x1=\"0\" y1=\"$i\" x2=\"1200\" y2=\"$i\" stroke=\"#333\" stroke-width=\"0.5\"/>"
  done
  for i in $(seq 100 100 1100); do
    echo "<line x1=\"$i\" y1=\"0\" x2=\"$i\" y2=\"800\" stroke=\"#333\" stroke-width=\"0.5\"/>"
  done
  echo '<text x="600" y="40" text-anchor="middle" fill="#eee" font-size="28" font-family="Arial">Cache Performance Dashboard</text>'
  echo '</svg>'
} > "$ASSET_DIR/dashboard-large.svg"

# ── 2. Generate PNG images using raw bytes ──
echo "→ Generating PNG images..."

# Use Python to create real PNG files of various sizes
python3 -c "
import struct, zlib, os

def create_png(width, height, filename, pattern='gradient'):
    \"\"\"Create a valid PNG file with real pixel data.\"\"\"
    def make_chunk(chunk_type, data):
        chunk = chunk_type + data
        return struct.pack('>I', len(data)) + chunk + struct.pack('>I', zlib.crc32(chunk) & 0xffffffff)

    # IHDR
    ihdr_data = struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0)  # 8-bit RGB
    ihdr = make_chunk(b'IHDR', ihdr_data)

    # IDAT - actual pixel data
    raw_data = bytearray()
    for y in range(height):
        raw_data.append(0)  # filter byte
        for x in range(width):
            if pattern == 'gradient':
                r = int(255 * x / width)
                g = int(255 * y / height)
                b = int(255 * (1 - x / width))
            elif pattern == 'noise':
                import hashlib
                h = hashlib.md5(f'{x},{y}'.encode()).digest()
                r, g, b = h[0], h[1], h[2]
            elif pattern == 'checker':
                if (x // 32 + y // 32) % 2 == 0:
                    r, g, b = 102, 126, 234
                else:
                    r, g, b = 118, 75, 162
            elif pattern == 'photo':
                # Simulate photo-like content with smooth gradients + noise
                import math, hashlib
                base_r = int(128 + 127 * math.sin(x * 0.02 + y * 0.01))
                base_g = int(128 + 127 * math.sin(x * 0.015 - y * 0.02))
                base_b = int(128 + 127 * math.cos(x * 0.01 + y * 0.015))
                h = hashlib.md5(f'{x},{y}'.encode()).digest()
                noise = (h[0] - 128) // 8
                r = max(0, min(255, base_r + noise))
                g = max(0, min(255, base_g + noise))
                b = max(0, min(255, base_b + noise))
            raw_data.extend([r, g, b])

    compressed = zlib.compress(bytes(raw_data), 9)
    idat = make_chunk(b'IDAT', compressed)

    # IEND
    iend = make_chunk(b'IEND', b'')

    # Write PNG
    with open(filename, 'wb') as f:
        f.write(b'\x89PNG\r\n\x1a\n')
        f.write(ihdr)
        f.write(idat)
        f.write(iend)

    size = os.path.getsize(filename)
    print(f'  Created {filename} ({width}x{height}) = {size:,} bytes')

# Small thumbnail (64x64) ~5KB
create_png(64, 64, '$ASSET_DIR/thumb-small.png', 'gradient')

# Medium icon (256x256) ~80KB
create_png(256, 256, '$ASSET_DIR/icon-medium.png', 'checker')

# Large image (800x600) ~500KB
create_png(800, 600, '$ASSET_DIR/photo-large.png', 'photo')

# HD image (1920x1080) ~2-3MB
create_png(1920, 1080, '$ASSET_DIR/banner-hd.png', 'photo')

# 4K image (3840x2160) ~10-15MB
create_png(3840, 2160, '$ASSET_DIR/hero-4k.png', 'gradient')
" 2>/dev/null || echo "  (Python PNG generation - using fallback)"

# ── 3. Generate JPEG-like files (binary content) ──
echo "→ Generating JPEG test files..."

python3 -c "
import struct, os

def create_jpeg_placeholder(width, height, quality, filename):
    \"\"\"Create a minimal valid JPEG file.\"\"\"
    # SOI marker
    data = bytearray(b'\xff\xd8')

    # APP0 (JFIF)
    app0 = b'\xff\xe0'
    jfif = b'JFIF\x00\x01\x02\x00\x00\x01\x00\x01\x00\x00'
    data += app0 + struct.pack('>H', len(jfif) + 2) + jfif

    # DQT (quantization table)
    data += b'\xff\xdb'
    qt = bytearray(65)
    qt[0] = 0  # table ID
    for i in range(1, 65):
        qt[i] = max(1, min(255, quality + (i * 2)))
    data += struct.pack('>H', len(qt) + 2) + bytes(qt)

    # SOF0 (Start of Frame)
    data += b'\xff\xc0'
    sof = struct.pack('>BHHB', 8, height, width, 3)  # 8-bit, YCbCr
    sof += b'\x01\x22\x00'  # Y: 2x2 sampling
    sof += b'\x02\x11\x00'  # Cb: 1x1
    sof += b'\x03\x11\x00'  # Cr: 1x1
    data += struct.pack('>H', len(sof) + 2) + sof

    # DHT (Huffman tables) - minimal
    data += b'\xff\xc4'
    ht = bytearray(29)
    ht[0] = 0x00  # DC table 0
    data += struct.pack('>H', len(ht) + 2) + bytes(ht)

    # SOS + scan data (fill with pseudo-random bytes to simulate real JPEG)
    data += b'\xff\xda'
    sos_header = struct.pack('>H', 12) + b'\x03\x01\x00\x02\x11\x03\x11\x00\x3f\x00'
    data += sos_header

    # Simulated compressed scan data
    import hashlib
    target_size = (width * height * 3) // (quality // 2 + 1)
    for chunk in range(0, target_size, 1024):
        h = hashlib.sha256(f'{chunk}'.encode()).digest()
        block = bytes([b if b != 0xff else 0xfe for b in h]) * 32
        data += block[:min(1024, target_size - chunk)]

    # EOI marker
    data += b'\xff\xd9'

    with open(filename, 'wb') as f:
        f.write(bytes(data))

    size = os.path.getsize(filename)
    print(f'  Created {filename} ({width}x{height} q={quality}) = {size:,} bytes')

create_jpeg_placeholder(640, 480, 85, '$ASSET_DIR/photo-medium.jpg')
create_jpeg_placeholder(1920, 1080, 80, '$ASSET_DIR/photo-hd.jpg')
create_jpeg_placeholder(3840, 2160, 75, '$ASSET_DIR/photo-4k.jpg')
" 2>/dev/null || echo "  (JPEG generation - using fallback)"

# ── 4. Generate fake video files (realistic sizes) ──
echo "→ Generating video test files..."

# 1MB video clip
dd if=/dev/urandom of="$ASSET_DIR/clip-1mb.mp4" bs=1024 count=1024 2>/dev/null
echo "  Created clip-1mb.mp4 = 1,048,576 bytes"

# 5MB video
dd if=/dev/urandom of="$ASSET_DIR/clip-5mb.mp4" bs=1024 count=5120 2>/dev/null
echo "  Created clip-5mb.mp4 = 5,242,880 bytes"

# 25MB video (HD clip)
dd if=/dev/urandom of="$ASSET_DIR/video-25mb.mp4" bs=1024 count=25600 2>/dev/null
echo "  Created video-25mb.mp4 = 26,214,400 bytes"

# ── 5. Generate CSS/JS bundles ──
echo "→ Generating CSS/JS bundles..."

# Large CSS bundle (~200KB)
{
  echo "/* Cache Server UI — Generated CSS Bundle */"
  for i in $(seq 1 2000); do
    r=$((RANDOM % 256))
    g=$((RANDOM % 256))
    b=$((RANDOM % 256))
    echo ".component-$i { color: rgb($r,$g,$b); padding: ${i}px; margin: $((i % 20))px; display: flex; align-items: center; justify-content: space-between; border-radius: $((i % 16))px; transition: all 0.3s ease; }"
  done
} > "$ASSET_DIR/bundle.css"

# Large JS bundle (~300KB)
{
  echo "/* Cache Server App — Generated JS Bundle */"
  echo "const APP_CONFIG = {"
  for i in $(seq 1 1000); do
    echo "  module_$i: { enabled: true, version: '$((RANDOM % 10)).$((RANDOM % 100)).$((RANDOM % 1000))', endpoints: ['api/v$((i % 5))/resource_$i'], timeout: $((RANDOM % 30000)), retries: $((RANDOM % 5)) },"
  done
  echo "};"
  for i in $(seq 1 1000); do
    echo "function handler_$i(req, res) { const data = JSON.parse(req.body); if (!data.id) throw new Error('missing id'); return { status: 200, body: { id: data.id, processed: true, timestamp: Date.now(), handler: 'handler_$i' } }; }"
  done
} > "$ASSET_DIR/bundle.js"

# ── 6. Generate font file (binary) ──
echo "→ Generating font test file..."
dd if=/dev/urandom of="$ASSET_DIR/custom-font.woff2" bs=1024 count=128 2>/dev/null
echo "  Created custom-font.woff2 = 131,072 bytes"

# ── Summary ──
echo ""
echo "══════════════════════════════════════"
echo "  Asset Generation Complete"
echo "══════════════════════════════════════"
echo ""
echo "Files created:"
ls -lhS "$ASSET_DIR/" | tail -n +2
echo ""
TOTAL=$(du -sh "$ASSET_DIR/" | cut -f1)
COUNT=$(ls -1 "$ASSET_DIR/" | wc -l | tr -d ' ')
echo "Total: $COUNT files, $TOTAL"
