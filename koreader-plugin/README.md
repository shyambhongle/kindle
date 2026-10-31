# Dashboard plugin for KOReader

A full screen, landscape dashboard inside KOReader, fed by the Go backend. Same screens as
the browser version (home, tasks, calendar day, guitar, German, vocab, reading, news), drawn
natively, with proper e-ink refreshes and no browser chrome.

```
dashboard.koplugin/
  _meta.lua              plugin id and description
  main.lua               KOReader glue: menu, settings, HTTP, rotation, painting, taps
  dashboard_render.lua   pure Lua layout: JSON + state -> draw ops + tap areas (testable anywhere)
```

## Install on the Kindle

1. Connect the Kindle over USB. KOReader lives at `koreader/` on the Kindle drive.
2. Copy the whole `dashboard.koplugin` folder into `koreader/plugins/`.
   You should end up with `koreader/plugins/dashboard.koplugin/main.lua`.
3. Eject, start KOReader (from KUAL), and restart KOReader once if it was already running.
4. Open the top menu > Tools (the wrench icon) > Dashboard > Set backend URL.
   Paste the API URL the backend prints at start, for example
   `http://192.168.1.20:8080/d/<your token>/api` (no trailing slash).
5. Tools > Dashboard > Open dashboard.

KOReader will turn Wi-Fi on if needed, rotate to landscape, fetch the JSON and draw the home
screen. When you leave, the rotation is restored.

## Using it

* Tap a task checkbox or a practice tile's box to mark it done (sent to the backend at once).
* Tap a panel to open its detail page. `< Home` goes back. `Refresh` fetches again.
* On the home screen, `Exit` closes the dashboard. A long press anywhere also goes back, and a
  long press on the home screen exits.
* On the calendar day page, `< Previous` and `Next >` move one day.
* On the guitar page, `Week N done >` advances the plan to the next week.
* `Tools > Dashboard > Rotate to landscape while open` can be turned off if rotation misbehaves
  on your firmware; the dashboard then draws scaled inside the portrait screen.

## Troubleshooting

* Nothing happens on tap: the plugin maps taps to the 1448 x 1072 canvas, scaled to the screen.
  If taps land in the wrong place, say which element you tapped and what opened.
* "Could not reach backend": check the URL ends in `/api`, the Kindle is on the same Wi-Fi,
  and the backend is listening on the LAN IP (not 127.0.0.1).
* Fonts look wrong: the plugin asks for NotoSerif and falls back to KOReader's UI font.
* KOReader logs to `koreader/crash.log`; lines starting with `dashboard:` are from this plugin.

## Developing

`dashboard_render.lua` has no KOReader dependencies. It takes the backend JSON plus a small
state table and returns a list of draw operations (rect, border, text, box) and tap rectangles.
That is what made it possible to test the layout off-device. To change the layout, edit that
file only; `main.lua` just paints what it returns.
