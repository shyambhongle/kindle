--[[
dashboard_render.lua: pure Lua layout for the dashboard. No KOReader dependencies, so it can be
tested anywhere. It turns backend JSON + UI state into a list of draw operations on a
1448 x 1072 canvas plus a list of tap rectangles.

ops:  {t="rect",   x,y,w,h, color}
      {t="border", x,y,w,h, bw, color}
      {t="text",   x,y, size, text, bold, color, maxw, align, font}      single line, clipped
      {t="box",    x,y,w,h, size, text, bold, color, font}               wrapped, clipped
hits: {x,y,w,h, action={type=..., ...}}
--]]

local R = {}

R.W, R.H = 1448, 1072

local DAYS = { "Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday" }
local DAYS3 = { "Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat" }
local MONTHS = { "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December" }
local MONTHS3 = { "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec" }

---------------------------------------------------------------- dates

-- "2026-10-31T11:00:00+05:30" -> {year,month,day,hour,min}; zone is ignored (backend sends local time)
function R.parseISO(s)
    if type(s) ~= "string" then return nil end
    local y, m, d, H, M = s:match("^(%d%d%d%d)%-(%d%d)%-(%d%d)T?(%d*):?(%d*)")
    if not y then return nil end
    local t = { year = tonumber(y), month = tonumber(m), day = tonumber(d), hour = tonumber(H) or 0, min = tonumber(M) or 0 }
    if s:sub(-1) == "Z" and H ~= "" then
        -- UTC timestamp: shift into the device's local time
        local utc = os.time({ year = t.year, month = t.month, day = t.day, hour = t.hour, min = t.min, sec = 0 })
        local offset = os.time(os.date("*t", utc)) - os.time(os.date("!*t", utc))
        local l = os.date("*t", utc + offset)
        t = { year = l.year, month = l.month, day = l.day, hour = l.hour, min = l.min }
    end
    return t
end

local function key(t) return string.format("%04d-%02d-%02d", t.year, t.month, t.day) end
local function stamp(t) return os.time({ year = t.year, month = t.month, day = t.day, hour = t.hour or 0, min = t.min or 0, sec = 0 }) end
local function hm(t) return string.format("%02d:%02d", t.hour, t.min) end
local function pad(n) return (n < 10 and "0" or "") .. n end

function R.today(now)
    local t = os.date("*t", now or os.time())
    return { year = t.year, month = t.month, day = t.day, hour = t.hour, min = t.min, wday = t.wday }
end

function R.dayOffset(base, n)
    local t = os.date("*t", os.time({ year = base.year, month = base.month, day = base.day, hour = 12 }) + n * 86400)
    return { year = t.year, month = t.month, day = t.day, hour = 0, min = 0, wday = t.wday }
end

local function eventsOn(data, day)
    local k, out = key(day), {}
    for _, ev in ipairs(data.events or {}) do
        local s = R.parseISO(ev.start)
        if s and key(s) == k then out[#out + 1] = ev end
    end
    table.sort(out, function(a, b) return a.start < b.start end)
    return out
end

local function nextEvent(data, now)
    local best, bt
    for _, ev in ipairs(data.events or {}) do
        local s, e = R.parseISO(ev.start), R.parseISO(ev["end"] or ev.start)
        if s and e and stamp(e) >= now then
            local st = stamp(s)
            if not best or st < bt then best, bt = ev, st end
        end
    end
    return best
end

---------------------------------------------------------------- op helpers

local Canvas = {}
Canvas.__index = Canvas

local function newCanvas()
    return setmetatable({ ops = {}, hits = {} }, Canvas)
end

function Canvas:rect(x, y, w, h, color)
    self.ops[#self.ops + 1] = { t = "rect", x = x, y = y, w = w, h = h, color = color or "black" }
end

function Canvas:border(x, y, w, h, bw, color)
    self.ops[#self.ops + 1] = { t = "border", x = x, y = y, w = w, h = h, bw = bw or 3, color = color or "black" }
end

-- single line
function Canvas:text(x, y, size, text, o)
    o = o or {}
    self.ops[#self.ops + 1] = { t = "text", x = x, y = y, size = size, text = tostring(text or ""),
        bold = o.bold or false, color = o.color or "black", maxw = o.maxw, align = o.align or "left", font = o.font or "serif" }
end

-- wrapped block
function Canvas:box(x, y, w, h, size, text, o)
    o = o or {}
    self.ops[#self.ops + 1] = { t = "box", x = x, y = y, w = w, h = h, size = size, text = tostring(text or ""),
        bold = o.bold or false, color = o.color or "black", font = o.font or "serif" }
end

function Canvas:hit(x, y, w, h, action)
    self.hits[#self.hits + 1] = { x = x, y = y, w = w, h = h, action = action }
end

-- panel with black header bar
function Canvas:panel(x, y, w, h, title, right)
    self:rect(x, y, w, h, "white")
    self:border(x, y, w, h, 3)
    if title then
        self:rect(x, y, w, 48, "black")
        self:text(x + 16, y + 11, 22, string.upper(title), { color = "white", font = "sans", maxw = w - 32 })
        if right then
            self:text(x + w - 16, y + 11, 22, string.upper(right), { color = "white", font = "sans", align = "right", maxw = w / 2 })
        end
    end
end

function Canvas:button(x, y, w, text, action, dark)
    self:rect(x, y, w, 56, dark and "black" or "white")
    self:border(x, y, w, 56, 3)
    self:text(x + w / 2, y + 13, 26, text, { align = "center", font = "sans", color = dark and "white" or "black", maxw = w - 12 })
    if action then self:hit(x - 10, y - 10, w + 20, 76, action) end
end

-- "n of last 7" squares
function Canvas:week(x, y, last7)
    local n = 0
    for i = 1, 7 do
        local v = (last7 and last7[i] == 1)
        if v then n = n + 1 end
        self:rect(x + (i - 1) * 30, y, 26, 26, v and "black" or "white")
        self:border(x + (i - 1) * 30, y, 26, 26, 3)
    end
    self:text(x + 215, y + 2, 20, n .. " of last 7", { font = "sans" })
end

function Canvas:checkbox(x, y, size, done)
    self:rect(x, y, size, size, done and "black" or "white")
    self:border(x, y, size, size, 4)
    if done then
        self:text(x + size / 2, y + size * 0.08, math.floor(size * 0.8), "✓", { color = "white", align = "center", font = "sans", bold = true, maxw = size })
    end
end

---------------------------------------------------------------- top bar

local function syncText(state)
    if not state.lastSync then return "Not synced" end
    return (state.syncOk and "Synced " or "Sync failed, showing ") .. state.lastSync
end

local function topbarHome(c, data, state, now)
    local t = R.today(now)
    c:rect(0, 80, R.W, 4, "black")
    c:text(24, 16, 40, string.format("%s, %d %s %d", DAYS[t.wday], t.day, MONTHS3[t.month], t.year), { bold = true, maxw = 600 })
    local w = data.weather or {}
    local wt = ""
    if w.tempC then wt = string.format("%d°", math.floor(w.tempC + 0.5)) end
    if w.summary and w.summary ~= "" then wt = wt .. "  " .. w.summary end
    if w.hiC then wt = wt .. string.format("  %d/%d°", math.floor(w.hiC + 0.5), math.floor((w.loC or 0) + 0.5)) end
    c:text(600, 20, 32, wt, { maxw = 400 })
    c:text(1120, 28, 24, syncText(state), { font = "sans", align = "right", maxw = 280 })
    c:button(1136, 12, 140, "Refresh", { type = "refresh" })
    c:button(1294, 12, 130, "Exit", { type = "close" })
end

local function topbarPage(c, state, title)
    c:rect(0, 80, R.W, 4, "black")
    c:button(24, 12, 150, "< Home", { type = "nav", page = "home" })
    c:text(200, 16, 40, title, { bold = true, maxw = 800 })
    c:text(1258, 28, 24, syncText(state), { font = "sans", align = "right", maxw = 300 })
    c:button(1284, 12, 140, "Refresh", { type = "refresh" })
end

---------------------------------------------------------------- home blocks

local function calendar(c, data, state, now)
    local t = R.today(now)
    local x, y, w, h = 24, 104, 440, 596
    c:panel(x, y, w, h, MONTHS[t.month] .. " " .. t.year)
    c:hit(x, y, w, 48, { type = "nav", page = "day", offset = 0 })
    local names = { "Mo", "Tu", "We", "Th", "Fr", "Sa", "Su" }
    local gx, gy, cw, ch = x + 10, y + 60, 60, 76
    for i = 1, 7 do
        c:text(gx + (i - 1) * cw + cw / 2, gy + 6, 20, names[i], { font = "sans", align = "center", maxw = cw })
    end
    c:rect(gx, gy + 36, 420, 2, "black")
    local first = os.date("*t", os.time({ year = t.year, month = t.month, day = 1, hour = 12 }))
    local startDow = (first.wday + 5) % 7 -- Monday = 0
    local base = R.dayOffset({ year = t.year, month = t.month, day = 1 }, -startDow)
    local todayKey = key(t)
    for r = 0, 5 do
        for col = 0, 6 do
            local d = R.dayOffset(base, r * 7 + col)
            local cx, cy = gx + col * cw, gy + 44 + r * ch
            local other = d.month ~= t.month
            local isToday = key(d) == todayKey
            if isToday then c:rect(cx + 7, cy + 8, 46, 46, "black") end
            c:text(cx + cw / 2, cy + 14, 28, tostring(d.day),
                { font = "sans", align = "center", color = isToday and "white" or (other and "gray" or "black"), bold = isToday, maxw = cw })
            if #eventsOn(data, d) > 0 then c:rect(cx + cw / 2 - 5, cy + 58, 10, 10, "black") end
            local off = math.floor((stamp({ year = d.year, month = d.month, day = d.day, hour = 12 }) - stamp({ year = t.year, month = t.month, day = t.day, hour = 12 })) / 86400 + 0.5)
            c:hit(cx, cy, cw, ch, { type = "nav", page = "day", offset = off })
        end
    end
end

local function nextUp(c, data, now)
    local x, y, w, h = 24, 716, 440, 130
    c:panel(x, y, w, h, "Next up")
    c:hit(x, y, w, h, { type = "nav", page = "day", offset = 0 })
    local ev = nextEvent(data, now)
    if not ev then
        c:text(x + 16, y + 56, 30, "Nothing scheduled", { bold = true, maxw = w - 32 })
        return
    end
    local s, e = R.parseISO(ev.start), R.parseISO(ev["end"])
    local t = R.today(now)
    local when
    if key(s) == key(t) then when = "Today"
    elseif key(s) == key(R.dayOffset(t, 1)) then when = "Tomorrow"
    else
        local sd = os.date("*t", stamp(s))
        when = DAYS3[sd.wday] .. " " .. s.day .. " " .. MONTHS3[s.month]
    end
    when = when .. " " .. hm(s) .. (e and (" to " .. hm(e)) or "")
    if ev.location and ev.location ~= "" then when = when .. "  |  " .. ev.location end
    c:text(x + 16, y + 56, 30, ev.title, { bold = true, maxw = w - 32 })
    c:text(x + 16, y + 94, 24, when, { font = "sans", maxw = w - 32 })
end

local function tasksHome(c, data)
    local x, y, w, h = 488, 104, 560, 742
    local top, done, total = {}, 0, 0
    for _, t in ipairs(data.tasks or {}) do
        if t.top and #top < 3 then top[#top + 1] = t end
        if t.done then done = done + 1 end
        total = total + 1
    end
    local topDone = 0
    for _, t in ipairs(top) do if t.done then topDone = topDone + 1 end end
    c:panel(x, y, w, h, "Top 3 today", topDone .. " of " .. #top .. " done")
    c:hit(x, y, w, h, { type = "nav", page = "tasks" })
    for i, t in ipairs(top) do
        local ry = y + 48 + (i - 1) * 180
        c:rect(x, ry + 177, w, 3, "black")
        c:checkbox(x + 18, ry + 22, 56, t.done)
        c:hit(x, ry, 100, 180, { type = "toggle_task", id = t.id, done = not t.done })
        c:box(x + 98, ry + 14, w - 114, 90, 34, t.title, { color = t.done and "gray" or "black" })
        local meta = t.project or ""
        if t.estimateMin and t.estimateMin > 0 then meta = meta .. "  |  ~" .. t.estimateMin .. " min" end
        c:text(x + 98, ry + 120, 24, meta, { font = "sans", color = "darkgray", maxw = w - 114 })
        if t.nextStep and t.nextStep ~= "" then
            c:text(x + 98, ry + 148, 22, "Next: " .. t.nextStep, { font = "sans", maxw = w - 114 })
        end
    end
    c:text(x + 16, y + h - 36, 22, string.format("%d open in total, %d done today. Tap for the full list.", total - done, done), { font = "sans", maxw = w - 32 })
end

local function tile(c, idx, keyName, name, done, sub, last7, extra)
    local x, y, w, h = 1072, 104 + idx * 190, 352, 172
    c:rect(x, y, w, h, "white")
    c:border(x, y, w, h, 3)
    c:hit(x, y, w, h, { type = "nav", page = keyName })
    c:text(x + 16, y + 12, 30, name, { bold = true, maxw = w - 90 })
    c:checkbox(x + w - 58, y + 10, 44, done)
    c:hit(x + w - 80, y, 80, 80, { type = "toggle_practice", key = keyName, done = not done })
    c:box(x + 16, y + 58, w - 32, 62, 24, sub, { font = "sans", color = "darkgray" })
    if extra then extra(x, y) else c:week(x + 16, y + 128, last7) end
end

local function tiles(c, data)
    local p = data.practice or {}
    local g, d, v, r = p.guitar or {}, p.german or {}, p.vocab or {}, p.reading or {}
    tile(c, 0, "guitar", "Guitar", g.doneToday, (g.current or "") .. (g.today and ("\n" .. g.today) or ""), g.last7)
    tile(c, 1, "german", "German" .. (d.level and (" " .. d.level) or ""), d.doneToday,
        ((d.dueReviews or 0) > 0 and (d.dueReviews .. " reviews due") or "No reviews due") .. "\nSentence below", d.last7)
    tile(c, 2, "vocab", "English vocab", v.doneToday,
        ((v.dueReviews or 0) > 0 and (v.dueReviews .. " words due") or "No words due") .. "\nWord of the day below", v.last7)
    local b = r.book or {}
    local pct = (b.pages and b.pages > 0) and math.floor(100 * (b.page or 0) / b.pages + 0.5) or 0
    tile(c, 3, "reading", "Reading", r.doneToday, (b.title or "No book") .. (b.author and ("\n" .. b.author) or ""), r.last7,
        function(x, y)
            c:border(x + 16, y + 128, 160, 22, 3)
            c:rect(x + 16, y + 128, math.floor(160 * pct / 100), 22, "black")
            c:text(x + 192, y + 124, 20, string.format("%d%%, p.%d/%d", pct, b.page or 0, b.pages or 0), { font = "sans", maxw = 150 })
        end)
end

local function pick(list, n)
    if not list or #list == 0 then return nil end
    return list[(n % #list) + 1]
end

local function learn(c, data, state)
    local p = data.practice or {}
    local s = pick((p.german or {}).sentences, state.refreshCount or 0)
    local w = pick((p.vocab or {}).words, state.refreshCount or 0)
    local x, y, width, h = 24, 870, state.showNews == false and 1400 or 1024, 178
    c:rect(x, y, width, h, "white")
    c:border(x, y, width, h, 3)
    local half = math.floor(width / 2) - 24
    c:text(x + 16, y + 14, 22, "GERMAN TODAY", { font = "sans" })
    c:hit(x, y, half, h, { type = "nav", page = "german" })
    if s then
        c:box(x + 16, y + 44, half - 16, 84, 34, s.de, { bold = true })
        c:text(x + 16, y + 134, 24, s.en, { color = "darkgray", maxw = half - 16 })
    end
    local ex = x + half + 32
    c:hit(ex, y, width - half - 32, h, { type = "nav", page = "vocab" })
    c:text(ex, y + 14, 22, "WORD OF THE DAY", { font = "sans" })
    if w then
        c:text(ex, y + 44, 34, w.word .. (w.pos and ("   " .. w.pos) or ""), { bold = true, maxw = width - half - 48 })
        c:box(ex, y + 90, width - half - 48, 80, 24, (w.definition or "") .. (w.example and ("  " .. w.example) or ""), { color = "darkgray" })
    end
end

local function newsHome(c, data, state)
    if state.showNews == false then return end
    local x, y, w, h = 1072, 870, 352, 178
    c:panel(x, y, w, h, "News")
    c:hit(x, y, w, h, { type = "nav", page = "news" })
    local list = data.news or {}
    for i = 1, math.min(2, #list) do
        local n = list[i]
        c:box(x + 14, y + 56 + (i - 1) * 62, w - 28, 58, 22, (n.source and (n.source .. ": ") or "") .. (n.title or ""))
    end
end

---------------------------------------------------------------- detail pages

local function row(c, x, y, w, h, title, sub)
    c:rect(x, y + h - 3, w, 3, "black")
    c:text(x + 20, y + 14, 32, title, { maxw = w - 40 })
    if sub and sub ~= "" then c:text(x + 20, y + 58, 24, sub, { font = "sans", color = "darkgray", maxw = w - 40 }) end
end

local function section(c, x, y, w, text)
    c:rect(x, y, w, 44, "black")
    c:text(x + 20, y + 9, 22, string.upper(text), { color = "white", font = "sans", maxw = w - 40 })
end

local function pageTasks(c, data, state)
    topbarPage(c, state, "Tasks")
    local x, y, w = 24, 104, 1400
    local top, rest = {}, {}
    for _, t in ipairs(data.tasks or {}) do
        if t.top then top[#top + 1] = t else rest[#rest + 1] = t end
    end
    local function list(items, label)
        section(c, x, y, w, label)
        y = y + 44
        for _, t in ipairs(items) do
            if y + 96 > R.H - 24 then return end
            c:checkbox(x + 20, y + 20, 48, t.done)
            c:hit(x, y, 96, 96, { type = "toggle_task", id = t.id, done = not t.done })
            c:rect(x, y + 93, w, 3, "black")
            c:text(x + 96, y + 12, 32, t.title, { color = t.done and "gray" or "black", maxw = w - 120 })
            local meta = t.project or ""
            if t.estimateMin and t.estimateMin > 0 then meta = meta .. "  |  ~" .. t.estimateMin .. " min" end
            if t.nextStep and t.nextStep ~= "" then meta = meta .. "  |  Next: " .. t.nextStep end
            c:text(x + 96, y + 56, 24, meta, { font = "sans", color = "darkgray", maxw = w - 120 })
            y = y + 96
        end
        if #items == 0 then row(c, x, y, w, 60, "Nothing here. Good."); y = y + 60 end
        y = y + 24
    end
    list(top, "Top 3 today")
    list(rest, "Next")
end

local function pageDay(c, data, state, now)
    topbarPage(c, state, "Calendar")
    local d = R.dayOffset(R.today(now), state.dayOffset or 0)
    local x, y, w = 24, 104, 1400
    local label = DAYS[d.wday] .. ", " .. d.day .. " " .. MONTHS[d.month] .. ((state.dayOffset or 0) == 0 and " (today)" or "")
    section(c, x, y, w, label)
    y = y + 44
    local list = eventsOn(data, d)
    if #list == 0 then row(c, x, y, w, 96, "No events. Free day.") end
    for _, ev in ipairs(list) do
        if y + 96 > R.H - 110 then break end
        local s, e = R.parseISO(ev.start), R.parseISO(ev["end"])
        row(c, x, y, w, 96, hm(s) .. (e and (" to " .. hm(e)) or "") .. "   " .. (ev.title or ""), ev.location)
        y = y + 96
    end
    c:button(24, R.H - 80, 220, "< Previous", { type = "day", delta = -1 })
    c:button(R.W - 244, R.H - 80, 220, "Next >", { type = "day", delta = 1 })
end

local function practiceFrame(c, state, title, keyName, done, doneLabel, last7, todoLabel)
    topbarPage(c, state, title)
    c:week(44, R.H - 128, last7)
    c:button(R.W - 244, R.H - 80, 220, done and doneLabel or (todoLabel or "Mark done"), { type = "toggle_practice", key = keyName, done = not done }, done)
end

local function pageGuitar(c, data, state)
    local g = (data.practice or {}).guitar or {}
    practiceFrame(c, state, "Guitar", "guitar", g.doneToday, "Done today", g.last7)
    local x, y, w = 24, 104, 700
    section(c, x, y, w, "Current"); y = y + 44
    c:box(x + 20, y + 16, w - 40, 110, 40, g.current or "", {})
    c:box(x + 20, y + 130, w - 40, 80, 28, g.today or "", { color = "darkgray" })
    y = y + 230
    section(c, x, y, w, "Notes"); y = y + 44
    c:box(x + 20, y + 16, w - 40, 200, 28, g.notes or "", { color = "darkgray" })
    local rx, ry, rw = 748, 104, 676
    section(c, rx, ry, rw, "Today's plan"); ry = ry + 44
    for i, step in ipairs(g.plan or {}) do
        if ry + 96 > R.H - 110 then break end
        c:rect(rx, ry + 93, rw, 3, "black")
        c:box(rx + 20, ry + 10, rw - 40, 80, 30, i .. ". " .. step, {})
        ry = ry + 96
    end
    c:button(24, R.H - 80, 220, "Week " .. (g.week or "?") .. " done >", { type = "guitar_advance", delta = 1 })
end

local function reviewPage(c, data, state, title, keyName, p, bigText, subText, dueLabel, items)
    practiceFrame(c, state, title, keyName, p.doneToday, "Done today", p.last7)
    local x, y, w = 24, 104, 700
    section(c, x, y, w, keyName == "german" and "Sentence of the day" or "Word of the day"); y = y + 44
    c:box(x + 20, y + 16, w - 40, 130, 40, bigText, {})
    c:box(x + 20, y + 150, w - 40, 120, 28, subText, { color = "darkgray" })
    c:text(x + 20, y + 320, 64, tostring(p.dueReviews or 0), { bold = true, font = "sans" })
    c:text(x + 20, y + 396, 22, string.upper(dueLabel), { font = "sans" })
    local rx, ry, rw = 748, 104, 676
    section(c, rx, ry, rw, "Review"); ry = ry + 44
    if #items == 0 then row(c, rx, ry, rw, 96, "Nothing due.") end
    for _, it in ipairs(items) do
        if ry + 96 > R.H - 110 then break end
        row(c, rx, ry, rw, 96, it[1], it[2])
        ry = ry + 96
    end
end

local function pageGerman(c, data, state)
    local g = (data.practice or {}).german or {}
    local s = pick(g.sentences, state.refreshCount or 0) or {}
    local items = {}
    for _, r in ipairs(g.review or {}) do items[#items + 1] = { r.de, r.en } end
    reviewPage(c, data, state, "German" .. (g.level and (" " .. g.level) or ""), "german", g, s.de or "", s.en or "", "Reviews due", items)
end

local function pageVocab(c, data, state)
    local v = (data.practice or {}).vocab or {}
    local w = pick(v.words, state.refreshCount or 0) or {}
    local items = {}
    for _, r in ipairs(v.review or {}) do items[#items + 1] = { r.word, r.definition } end
    reviewPage(c, data, state, "English vocabulary", "vocab", v, (w.word or "") .. (w.pos and ("   " .. w.pos) or ""),
        (w.definition or "") .. (w.example and ("\n" .. w.example) or ""), "Words due", items)
end

local function pageReading(c, data, state)
    local r = (data.practice or {}).reading or {}
    local b = r.book or {}
    practiceFrame(c, state, "Reading", "reading", r.doneToday, "Read today", r.last7, "Mark read")
    local x, y, w = 24, 104, 700
    section(c, x, y, w, "Now reading"); y = y + 44
    c:box(x + 20, y + 16, w - 40, 110, 40, b.title or "No book", {})
    c:text(x + 20, y + 130, 28, b.author or "", { color = "darkgray", maxw = w - 40 })
    local pct = (b.pages and b.pages > 0) and math.floor(100 * (b.page or 0) / b.pages + 0.5) or 0
    c:border(x + 20, y + 200, w - 40, 40, 4)
    c:rect(x + 20, y + 200, math.floor((w - 40) * pct / 100), 40, "black")
    local left = (b.pages or 0) - (b.page or 0)
    local total, n = 0, 0
    for _, s in ipairs(r.sessions or {}) do total = total + (s.pages or 0); n = n + 1 end
    local avg = n > 0 and math.max(1, math.floor(total / n + 0.5)) or 0
    local daysLeft = avg > 0 and math.ceil(left / avg) or 0
    local stats = { { pct .. "%", string.format("page %d of %d", b.page or 0, b.pages or 0) }, { tostring(left), "pages left" }, { avg > 0 and tostring(daysLeft) or "-", "days left at " .. avg .. " p/d" } }
    for i, s in ipairs(stats) do
        local sx = x + 20 + (i - 1) * 230
        c:text(sx, y + 270, 64, s[1], { bold = true, font = "sans", maxw = 220 })
        c:text(sx, y + 346, 22, string.upper(s[2]), { font = "sans", maxw = 220 })
    end
    local rx, ry, rw = 748, 104, 676
    section(c, rx, ry, rw, "Recent sessions"); ry = ry + 44
    if #(r.sessions or {}) == 0 then row(c, rx, ry, rw, 96, "No sessions yet."); ry = ry + 96 end
    for i, s in ipairs(r.sessions or {}) do
        if i > 4 then break end
        local d = R.parseISO(s.date)
        local label = d and (DAYS3[os.date("*t", stamp(d)).wday] .. " " .. d.day .. " " .. MONTHS3[d.month]) or (s.date or "")
        row(c, rx, ry, rw, 96, label, (s.pages or 0) .. " pages" .. (s.minutes and (", " .. s.minutes .. " min") or ""))
        ry = ry + 96
    end
    ry = ry + 20
    section(c, rx, ry, rw, "Up next"); ry = ry + 44
    for i, nb in ipairs(r.next or {}) do
        if i > 2 or ry + 96 > R.H - 110 then break end
        row(c, rx, ry, rw, 96, nb.title or "", nb.author)
        ry = ry + 96
    end
end

local function pageNews(c, data, state)
    topbarPage(c, state, "News")
    local x, y, w = 24, 104, 1400
    local list = data.news or {}
    if #list == 0 then row(c, x, y, w, 96, "No news. Also fine.") end
    for _, n in ipairs(list) do
        if y + 110 > R.H - 24 then break end
        c:rect(x, y + 107, w, 3, "black")
        c:text(x + 20, y + 12, 30, n.title or "", { maxw = w - 40 })
        c:box(x + 20, y + 54, w - 40, 50, 22, (n.source and (n.source .. "  |  ") or "") .. (n.summary or ""), { font = "sans", color = "darkgray" })
        y = y + 110
    end
end

---------------------------------------------------------------- entry

function R.render(data, state, now)
    now = now or os.time()
    data = data or {}
    local c = newCanvas()
    c:rect(0, 0, R.W, R.H, "white")
    local page = state.page or "home"
    if page == "home" then
        topbarHome(c, data, state, now)
        calendar(c, data, state, now)
        nextUp(c, data, now)
        tasksHome(c, data)
        tiles(c, data)
        learn(c, data, state)
        newsHome(c, data, state)
        if state.error then c:text(24, 1046, 20, state.error, { font = "sans", maxw = 1000 }) end
    elseif page == "tasks" then pageTasks(c, data, state)
    elseif page == "day" then pageDay(c, data, state, now)
    elseif page == "guitar" then pageGuitar(c, data, state)
    elseif page == "german" then pageGerman(c, data, state)
    elseif page == "vocab" then pageVocab(c, data, state)
    elseif page == "reading" then pageReading(c, data, state)
    elseif page == "news" then pageNews(c, data, state)
    else
        topbarPage(c, state, page)
    end
    return c.ops, c.hits
end

-- returns the action for a tap at canvas coordinates, last-added wins (buttons sit on top of panels)
function R.hitTest(hits, x, y)
    for i = #hits, 1, -1 do
        local h = hits[i]
        if x >= h.x and x < h.x + h.w and y >= h.y and y < h.y + h.h then return h.action end
    end
    return nil
end

return R
