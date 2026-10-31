# Checking the Kindle layout without a Kindle

`dashboard_render.lua` has no KOReader dependencies: it turns the backend snapshot plus a small
state table into draw operations. That makes the layout checkable on any machine with Lua.

```
brew install lua
cd koreader-plugin/tools
lua robust.lua                 # every page against empty and half built snapshots
mkdir -p out && lua preview.lua out   # every page to out/page-<name>.svg
```

`preview.lua` reports anything drawn outside the 1448 x 1072 canvas and writes one SVG per
page. To look at them as the Kindle would:

```
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless --disable-gpu \
  --screenshot=out/home.png --window-size=1448,1072 --hide-scrollbars "file://$PWD/out/page-home.svg"
```

Two things the preview does not do, so read it with that in mind:

* It does not clip text to `maxw`. Where a line runs past its panel in the SVG, the device
  truncates it with an ellipsis instead. Treat overflow as a warning, not as what you will see.
* Its line wrapping is a character count estimate. KOReader's `TextBoxWidget` measures the real
  font, so it usually fits slightly more per line than the preview shows.

`mock.lua` is a full snapshot: forecast for several places, a flagged security item, a German
course mid quiz, an English pack and a book summary long enough to page. Keep it in step with
`backend/internal/model/model.go` when the snapshot changes.

`luac -p` checks syntax, but Homebrew's Lua is 5.5 while KOReader runs LuaJIT (5.1). It will
not catch 5.1 problems, so keep `dashboard_render.lua` pure ASCII and avoid `\u{...}` escapes.
