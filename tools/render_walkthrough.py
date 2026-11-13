"""Render real Go TUI fixture frames as an explicitly simulated MP4 walkthrough.
Requires: pillow, pyte, imageio-ffmpeg. No live servers or credentials are used.
"""
import json
import subprocess
import sys
from pathlib import Path
import imageio_ffmpeg
import pyte
from PIL import Image, ImageDraw, ImageFont

frames = json.loads(Path(sys.argv[1]).read_text())
output = Path(sys.argv[2])
output.parent.mkdir(parents=True, exist_ok=True)
font_path = '/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf'
font = ImageFont.truetype(font_path, 17)
heading = ImageFont.truetype(font_path, 22)
width, height = 1280, 900
process = subprocess.Popen([imageio_ffmpeg.get_ffmpeg_exe(), '-y', '-loglevel', 'error', '-f', 'rawvideo', '-pixel_format', 'rgb24', '-video_size', f'{width}x{height}', '-framerate', '4', '-i', '-', '-an', '-c:v', 'libx264', '-crf', '22', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', str(output)], stdin=subprocess.PIPE)
colors = {'default': '#eeeeee', 'black':'#101010','red':'#ef4444','green':'#95ff00','yellow':'#f59e0b','blue':'#60a5fa','magenta':'#c084fc','cyan':'#67e8f9','white':'#eeeeee'}
for item in frames:
    screen = pyte.Screen(110, 40)
    pyte.Stream(screen).feed(item['Content'].replace('\n','\r\n'))
    im = Image.new('RGB', (width,height), '#101010')
    draw = ImageDraw.Draw(im)
    draw.rounded_rectangle((22,18,1258,65), radius=10, fill='#95ff00')
    draw.text((38,29),'KORA  /  OFFLINE WALKTHROUGH — DEMO DATA',font=heading,fill='#101010')
    draw.text((38,79),item['Title'],font=heading,fill='#f5f5f5')
    for y,row in screen.buffer.items():
        for x,char in row.items():
            if char.bg != 'default':
                bg=colors.get(char.bg, '#'+char.bg if len(char.bg)==6 else '#101010')
                draw.rectangle((38+x*10.3,120+y*18,38+(x+1)*10.3,120+(y+1)*18),fill=bg)
            color=colors.get(char.fg, '#'+char.fg if len(char.fg)==6 else '#eeeeee')
            draw.text((38+x*10.3,120+y*18), char.data, font=font, fill=color)
    draw.text((38,866),'Real TUI rendering • simulated checks/install • no live credentials or servers',font=font,fill='#949494')
    # Four animation frames per scene, each held for 0.75 seconds.
    for _ in range(3): process.stdin.write(im.tobytes())
process.stdin.close()
if process.wait(): raise SystemExit('Video encoder failed')
print(output)
