# Kindle Dashboard (client)

A single-file web app for a jailbroken Kindle Paperwhite 3 (7th gen, 1072 x 1448, 300 ppi).
It runs in the Kindle's built-in browser, in landscape, and shows one calm home screen with
tap-through detail pages. All data comes from your backend as one JSON document.

## Files

| File | What it is |
|---|---|
| `index.html` | The whole app. Plain ES5 and simple CSS so the old Kindle WebKit can run it. |
| `mock-data.json` | The exact JSON shape the client expects. Use it as the contract for your backend. |
| `kual/dashboard/` | A KUAL extension that opens the app from the Kindle's KUAL menu. |

## Home screen

Designed on a 1448 x 1072 canvas, rotated and scaled to the portrait screen at runtime.

* Top bar: date, weather for today, last sync status, Refresh button.
* Left: month calendar (dots on days with events, today filled) and "Next up" (next event with time).
* Middle: Top 3 tasks with checkbox, project, estimate, and the next physical step.
* Right: four practice tiles (Guitar, German, English vocab, Reading), each with a done-today box and "n of last 7" squares. Reading shows a progress bar.
* Bottom: German sentence of the day and English word of the day (rotate on each refresh), plus two news headlines.

Tapping a block opens its detail page. Every page has a "< Home" button and Refresh.

## Config

At the top of the `<script>` in `index.html`:

```js
var CONFIG = {
  apiUrl: "",            // "" = built-in mock data. Set to your backend, e.g. "http://192.168.1.20:8080/api/dashboard"
  landscape: true,       // rotate to landscape
  rotation: 90,          // 90 = Kindle's bottom bezel on the left; -90 = on the right
  showNews: true,        // false hides the news block
  flashOnRefresh: true,  // 300 ms black flash before redraw to clear e-ink ghosting
  weekStartsMonday: true
};
```

## Backend contract

`GET {apiUrl}` must return JSON in the shape of `mock-data.json`:

* `generatedAt` ISO timestamp
* `weather` `{ tempC, hiC, loC, summary, location }`
* `events[]` `{ id, title, start, end, location }` with ISO dates (with or without timezone)
* `tasks[]` `{ id, title, project, estimateMin, done, top, nextStep }` where `top: true` marks the Top 3
* `practice.guitar` `{ doneToday, last7[7], current, today, plan[], notes }`
* `practice.german` `{ doneToday, last7, level, dueReviews, sentences[{de,en}], review[{de,en}] }`
* `practice.vocab` `{ doneToday, last7, dueReviews, words[{word,pos,definition,example}], review[{word,definition}] }`
* `practice.reading` `{ doneToday, last7, book{title,author,page,pages}, sessions[{date,pages,minutes}], next[{title,author}] }`
* `news[]` `{ id, source, title, summary }`

`last7` is an array of 7 values, oldest first, last one is today. 1 = done, 0 = not done.

Writes the client sends (only when `apiUrl` is set):

* `POST {apiUrl}/tasks/{id}/done` body `{ "done": true|false }`
* `POST {apiUrl}/practice/{guitar|german|vocab|reading}/done` body `{ "done": true|false }`

The client updates the screen first, then posts. If the backend is unreachable, the top bar shows
"Sync failed, showing HH:MM" and the last good data stays on screen.

If your backend runs on another host or port, send CORS headers (`Access-Control-Allow-Origin: *`),
or simply serve `index.html` from the same backend so there is no cross-origin request at all.
Serving it from the backend is the easiest path: one URL, and updates need no file copying.

## Install on the Kindle

### Option A: serve from your backend (recommended)

1. Put `index.html` in your backend's static folder, served at e.g. `http://192.168.1.20:8080/`.
2. Set `CONFIG.apiUrl` to the API path.
3. On the Kindle, open the Experimental Browser and go to that URL once. Bookmark it.

### Option B: local file

1. Copy `index.html` to the Kindle over USB into `/mnt/us/dashboard/index.html`.
2. In the browser go to `file:///mnt/us/dashboard/index.html`.
3. With `apiUrl` set, the page loads locally and fetches data over Wi-Fi (CORS headers needed).

### KUAL launcher

Copy the `kual/dashboard` folder into `/mnt/us/extensions/` on the Kindle. KUAL then shows a
"Dashboard" entry that opens the browser at the URL in `kual/dashboard/bin/open.sh`. Edit that URL first.
The launcher uses `lipc-set-prop com.lab126.appmgrd start app://com.lab126.browser?url=...`, which is
the common way to open the browser from a script on 5.x firmware. If your firmware ignores the URL
part, the browser still opens and the bookmark from step 3 is one tap away.

## E-ink notes baked in

* Black on white only, 3 to 4 px borders, no gray text smaller than 20 px, no gradients, no animation.
* Pages never scroll. Lists are cut to what fits; the day page pages with Previous/Next buttons.
* Refresh is manual (the button). Each refresh flashes the screen black for 300 ms to clear ghosting.
* The sentence and word of the day rotate through the lists on each refresh, so you get passive repetition.

## Testing on a laptop

Open `index.html` in any browser and resize the window to a portrait shape (for example 536 x 724).
The canvas rotates and scales to fit. Chrome's device toolbar with a custom 1072 x 1448 device
is closest to the real thing.
