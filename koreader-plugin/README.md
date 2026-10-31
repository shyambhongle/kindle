# Dashboard plugin for KOReader

A full screen, landscape dashboard inside KOReader, fed by the Go backend. Drawn natively,
with proper e-ink refreshes and no browser chrome.

Screens: home, tasks, calendar day, weather, guitar, German, English, vocabulary, reading,
book summary, read-aloud passage, news and security.

```
dashboard.koplugin/
  _meta.lua              plugin id and description
  main.lua               KOReader glue: menu, settings, HTTP, rotation, painting, taps
  dashboard_render.lua   pure Lua layout: JSON + state -> draw ops + tap areas (testable anywhere)
tools/                   off-device layout check: mock snapshot, SVG preview, robustness run
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

## The home screen

A 1448 x 1072 canvas, two columns, nothing scrolls.

Left column:

* Header: the date, then today's weather as `24 deg  Partly cloudy  31/21 deg  rain 20%`.
  Tapping the weather line opens the weather page. Sync status, `Refresh` and `Exit` sit on
  the right of the header.
* Task board: the Top 3 with checkbox, project, estimate and next step, a `NEXT` column beside
  them, and a footer with the real counts from `taskCounts`, including the backlog the Kindle
  never receives.
* News board: Hacker News in one column, the curated general feeds in the other. When a
  security item is flagged, a black banner takes the top of the board and opens the security
  page.

Right column:

* The month, today filled, a dot on any day with an event. Tap a day to open it.
* Four practice buttons (German, Guitar, Reading, English). Each carries today's state, for
  example `Quiz 12 of 14` or `Day 12: Deep Work`, and a box showing whether it is done. The
  whole button opens the page; marking done happens there, so a mis-tap cannot mark anything.
* Today: what is left on the calendar today, with times.

Vocabulary has no button of its own. It is reached from the English page.

## Using it

* Tap a task checkbox to mark it done (sent to the backend at once).
* Tap a panel or a button to open its page. `< Home` goes back. `Refresh` fetches again.
* On the home screen, `Exit` closes the dashboard. A long press anywhere also goes back, and a
  long press on the home screen exits.
* On the calendar day page, `< Previous` and `Next >` move one day.
* On the guitar page, `Week N done >` advances the plan to the next week.
* The book summary and the read-aloud passage open in a reader. `< Back` and `Next >` turn the
  page, the counter in the header shows where you are, and the middle button marks it read.
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

Two rules when editing it:

* Keep the file pure ASCII. KOReader runs LuaJIT, which is Lua 5.1 and has no `\u{...}`
  escapes, so symbols go in as decimal byte escapes (`"\194\176"` is a degree sign).
* There is no way to measure text in pure Lua. The reader therefore pages long text by
  character budget (`READER_BUDGET`), set low on purpose: a short page is better than a page
  whose last lines are silently cut off.

The state table the renderer takes is
`{ page, dayOffset, refreshCount, readerPage, lastSync, syncOk, error }`.

To check a layout without a Kindle, use `tools/` (see `tools/README.md`): `lua robust.lua`
renders every page against empty and half built snapshots, and `lua preview.lua out` writes one
SVG per page and reports anything drawn outside the canvas.
