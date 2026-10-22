# How-it-works diagram

`how-it-works.html` is the source for `docs/how-it-works.gif`. Open it in a
browser and add `#t=2.5` to the URL to see the frame at 2.5s (the loop is 7.2s).

To re-record (needs Google Chrome and ffmpeg):

```sh
D=$(pwd)/docs/diagram   # render_frame.sh expects how.html in $D
cp $D/how-it-works.html $D/how.html && mkdir -p $D/frames
seq 0 143 | xargs -P 8 -I{} $D/render_frame.sh $D {}
ffmpeg -y -framerate 20 -i $D/frames/f%03d.png \
  -vf "fps=15,scale=1200:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96:stats_mode=diff[p];[b][p]paletteuse=dither=none:diff_mode=rectangle" \
  -loop 0 docs/how-it-works.gif
rm -rf $D/frames $D/how.html
```
