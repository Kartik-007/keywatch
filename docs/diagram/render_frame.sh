#!/bin/sh
# usage: render_frame.sh <dir> <index>
D=$1; i=$2
t=$(echo "scale=4; $i/20" | bc); f=$(printf "%03d" $i)
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu --hide-scrollbars \
  --force-device-scale-factor=1.2 --window-size=1600,900 --virtual-time-budget=4000 \
  --user-data-dir=$D/prof$i --screenshot=$D/frames/f$f.png "file://$D/how.html#t=$t" >/dev/null 2>&1
rm -rf $D/prof$i
