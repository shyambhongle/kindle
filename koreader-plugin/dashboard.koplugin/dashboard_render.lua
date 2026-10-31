--[[
dashboard_render.lua: pure Lua layout for the dashboard. No KOReader dependencies, so it can be
tested anywhere. It turns backend JSON + UI state into a list of draw operations on a
1448 x 1072 canvas plus a list of tap rectangles.

ops:  {t="rect",   x,y,w,h, color}
      {t="border", x,y,w,h, bw, color}
      {t="text",   x,y, size, text, bold, color, maxw, align, font}      single line, clipped
      {t="box",    x,y,w,h, size, text, bold, color, font}               wrapped, clipped
hits: {x,y,w,h, action={type=..., ...}}

Home grid (see README): a left column 1012 wide holding the header, the task board and the
news board, and a right column 368 wide holding the month, four practice buttons and today's
events. Nothing on the home screen scrolls; everything is one tap away.
--]]

local R = {}

R.W, R.H = 1448, 1072

-- home grid
local LX, LW = 24, 1012     -- left column
local RX, RW = 1056, 368    -- right column

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
local function round(n) return math.floor((n or 0) + 0.5) end

function R.today(now)
    local t = os.date("*t", now or os.time())
    return { year = t.year, month = t.month, day = t.day, hour = t.hour, min = t.min, wday = t.wday }
end

function R.dayOffset(base, n)
    local t = os.date("*t", os.time({ year = base.year, month = base.month, day = base.day, hour = 12 }) + n * 86400)
    return { year = t.year, month = t.month, day = t.day, hour = 0, min = 0, wday = t.wday }
end

-- an event covers a day if the day falls between its start and end dates (all-day and
-- multi-day events span more than the day they begin on)
local function coversDay(ev, day)
    local s = R.parseISO(ev.start)
    if not s then return false end
    local e = R.parseISO(ev["end"]) or s
    local d = stamp({ year = day.year, month = day.month, day = day.day, hour = 12 })
    local sd = stamp({ year = s.year, month = s.month, day = s.day, hour = 0 })
    local ed = stamp({ year = e.year, month = e.month, day = e.day, hour = 0 })
    -- an end at exactly midnight belongs to the previous day
    if ed > sd and e.hour == 0 and e.min == 0 then ed = ed - 86400 end
    return d >= sd and d <= ed + 43200
end

local function eventsOn(data, day)
    local out = {}
    for _, ev in ipairs(data.events or {}) do
        if coversDay(ev, day) then out[#out + 1] = ev end
    end
    table.sort(out, function(a, b) return (a.start or "") < (b.start or "") end)
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
        self:text(x + size / 2, y + size * 0.08, math.floor(size * 0.8), "\226\156\147", { color = "white", align = "center", font = "sans", bold = true, maxw = size })
    end
end

---------------------------------------------------------------- reading the snapshot

local function practice(data, k) return ((data.practice or {})[k]) or {} end

local function homePlace(data)
    for _, pf in ipairs(data.forecast or {}) do
        if pf.place and pf.place.home then return pf end
    end
    return (data.forecast or {})[1]
end

-- "24 deg   Partly cloudy   31/21 deg   rain 20%"
local function weatherLine(data)
    local w = data.weather or {}
    local pf = homePlace(data)
    local cur = (pf and pf.current) or {}
    local d1 = pf and (pf.days or {})[1] or nil
    local parts = {}
    local t = w.tempC or cur.tempC
    if t then parts[#parts + 1] = string.format("%d\194\176", round(t)) end
    local s = w.summary
    if not s or s == "" then s = cur.summary end
    if s and s ~= "" then parts[#parts + 1] = s end
    local hi, lo = w.hiC, w.loC
    if d1 then hi = hi or d1.hiC; lo = lo or d1.loC end
    if hi then parts[#parts + 1] = string.format("%d/%d\194\176", round(hi), round(lo)) end
    if d1 and d1.rainPct then parts[#parts + 1] = "rain " .. d1.rainPct .. "%" end
    if #parts == 0 then return "Weather not available" end
    return table.concat(parts, "   ")
end

local function alerts(data)
    local out = {}
    for _, n in ipairs(data.security or {}) do
        if n.alert then out[#out + 1] = n end
    end
    return out
end

-- one line of state for each practice button
local function germanLine(data)
    local g = practice(data, "german")
    local co = g.course
    if co and co.started then
        local r = co.todayResult
        if r then return "Quiz " .. (r.score or 0) .. " of " .. (r.total or 0) end
        local wr = co.written
        if wr and co.weekday == "Saturday" then
            if wr.marking then return "Test marked " .. (wr.marking.score or 0) .. "/100" end
            if wr.status == "ready" then return "Written test, week " .. (co.week or 0) end
        end
        local q = co.quiz
        if q and q.status == "ready" then return "Quiz ready, " .. #(q.questions or {}) .. " questions" end
        if q and q.status == "error" then return "Quiz failed to generate" end
        return "Day " .. (co.day or 0) .. ", week " .. (co.week or 0)
    end
    local n = g.dueReviews or 0
    if n > 0 then return n .. " reviews due" end
    return "No reviews due"
end

local function guitarLine(data)
    local g = practice(data, "guitar")
    local cur = g.current or ""
    if g.week and cur ~= "" then return "Wk " .. g.week .. " " .. cur end
    if cur ~= "" then return cur end
    if g.week then return "Week " .. g.week end
    return "No plan yet"
end

local function readingLine(data)
    local r = practice(data, "reading")
    local s = r.summary
    if s and s.today then return "Day " .. (s.day or 0) .. ": " .. (s.today.title or "") end
    if s and s.finished then return "Plan finished, add more books" end
    local b = r.book or {}
    if b.title and b.title ~= "" then return b.title end
    return "No book today"
end

local function englishPack(data)
    local e = practice(data, "english")
    return e.today
end

local function englishLine(data)
    local e = practice(data, "english")
    if not e.generator then return "Not set up" end
    local t = e.today
    if not t then return "Nothing for today" end
    if t.status ~= "ready" then return "Not generated yet" end
    local p = t.passage or {}
    return #(t.phrases or {}) .. " phrases, " .. (p.wordCount or 0) .. " word passage"
end

---------------------------------------------------------------- home

local function syncText(state)
    if not state.lastSync then return "Not synced" end
    return (state.syncOk and "Synced " or "Sync failed, showing ") .. state.lastSync
end

local function header(c, data, state, now)
    local t = R.today(now)
    c:text(28, 20, 42, string.format("%s, %d %s %d", DAYS[t.wday], t.day, MONTHS3[t.month], t.year), { bold = true, maxw = 700 })
    c:text(28, 84, 28, weatherLine(data), { maxw = 700 })
    c:hit(LX, 76, 700, 54, { type = "nav", page = "forecast" })
    c:text(LX + LW, 24, 22, syncText(state), { font = "sans", align = "right", maxw = 300 })
    c:button(746, 62, 140, "Refresh", { type = "refresh" })
    c:button(896, 62, 140, "Exit", { type = "close" })
    c:rect(LX, 144, LW, 3, "black")
end

local function calendarPanel(c, data, now)
    local t = R.today(now)
    local x, y, w, h = RX, 24, RW, 400
    c:panel(x, y, w, h, MONTHS[t.month] .. " " .. t.year)
    c:hit(x, y, w, 48, { type = "nav", page = "day", offset = 0 })
    local names = { "Mo", "Tu", "We", "Th", "Fr", "Sa", "Su" }
    local gx, cw = x + 9, 50
    for i = 1, 7 do
        c:text(gx + (i - 1) * cw + cw / 2, y + 56, 20, names[i], { font = "sans", align = "center", maxw = cw })
    end
    c:rect(gx, y + 86, 7 * cw, 2, "black")
    local first = os.date("*t", os.time({ year = t.year, month = t.month, day = 1, hour = 12 }))
    local startDow = (first.wday + 5) % 7 -- Monday = 0
    local base = R.dayOffset({ year = t.year, month = t.month, day = 1 }, -startDow)
    local todayKey = key(t)
    local todayStamp = stamp({ year = t.year, month = t.month, day = t.day, hour = 12 })
    for r = 0, 5 do
        for col = 0, 6 do
            local d = R.dayOffset(base, r * 7 + col)
            local cx, cy = gx + col * cw, y + 92 + r * 50
            local other = d.month ~= t.month
            local isToday = key(d) == todayKey
            if isToday then c:rect(cx + 7, cy + 2, 36, 36, "black") end
            c:text(cx + cw / 2, cy + 6, 26, tostring(d.day),
                { font = "sans", align = "center", color = isToday and "white" or (other and "gray" or "black"), bold = isToday, maxw = cw })
            if #eventsOn(data, d) > 0 then c:rect(cx + cw / 2 - 4, cy + 40, 8, 8, isToday and "black" or (other and "gray" or "black")) end
            local off = math.floor((stamp({ year = d.year, month = d.month, day = d.day, hour = 12 }) - todayStamp) / 86400 + 0.5)
            c:hit(cx, cy, cw, 50, { type = "nav", page = "day", offset = off })
        end
    end
end

local function taskPanel(c, data)
    local x, y, w, h = LX, 160, LW, 556
    local top, rest = {}, {}
    for _, t in ipairs(data.tasks or {}) do
        if t.top then
            if #top < 3 then top[#top + 1] = t end
        elseif not t.done then
            rest[#rest + 1] = t
        end
    end
    local topDone = 0
    for _, t in ipairs(top) do if t.done then topDone = topDone + 1 end end
    c:panel(x, y, w, h, "Top 3 today", topDone .. " of " .. #top .. " done")
    c:hit(x, y, w, h, { type = "nav", page = "tasks" })

    local splitX, footY = x + 616, y + h - 46
    c:rect(splitX, y + 48, 3, footY - y - 48, "black")

    -- Top 3, left
    if #top == 0 then
        c:text(x + 20, y + 80, 32, "No top tasks. Pick three.", { color = "darkgray", maxw = 576 })
    end
    for i, t in ipairs(top) do
        local ry = y + 48 + (i - 1) * 154
        c:checkbox(x + 20, ry + 18, 52, t.done)
        c:hit(x, ry, 92, 154, { type = "toggle_task", id = t.id, done = not t.done })
        c:box(x + 92, ry + 10, 508, 78, 32, t.title, { color = t.done and "gray" or "black" })
        local meta = t.project or ""
        if t.estimateMin and t.estimateMin > 0 then meta = meta .. "  |  ~" .. t.estimateMin .. " min" end
        if t.due and t.due ~= "" then meta = meta .. "  |  due " .. t.due end
        c:text(x + 92, ry + 94, 22, meta, { font = "sans", color = "darkgray", maxw = 508 })
        if t.nextStep and t.nextStep ~= "" then
            c:text(x + 92, ry + 122, 22, "Next: " .. t.nextStep, { font = "sans", maxw = 508 })
        end
        if i < 3 then c:rect(x, ry + 151, 616, 3, "black") end
    end

    -- Next, right
    local rx, rw = splitX + 3, w - 619
    c:text(rx + 20, y + 54, 20, "NEXT", { font = "sans", maxw = rw - 40 })
    c:rect(rx + 20, y + 82, rw - 40, 2, "black")
    local ry = y + 90
    for i, t in ipairs(rest) do
        if i > 5 then break end
        c:box(rx + 20, ry + 4, rw - 40, 54, 23, t.title, {})
        c:text(rx + 20, ry + 60, 19, t.project or "", { font = "sans", color = "darkgray", maxw = rw - 40 })
        c:rect(rx + 20, ry + 78, rw - 40, 2, "lightgray")
        ry = ry + 80
    end
    if #rest == 0 then c:text(rx + 20, ry + 6, 26, "Nothing queued.", { color = "darkgray", maxw = rw - 40 }) end

    -- counts straight from the board, including the tasks the Kindle never sees
    local tc = data.taskCounts or {}
    local line = string.format("%d top   %d next   %d backlog   %d done today",
        tc.top or #top, tc.next or #rest, tc.backlog or 0, tc.doneToday or 0)
    c:rect(x, footY, w, 3, "black")
    c:text(x + 20, footY + 12, 22, line, { font = "sans", maxw = w - 40 })
end

local BUTTONS = {
    { page = "german", name = "German", line = germanLine, done = function(d) return practice(d, "german").doneToday end },
    { page = "guitar", name = "Guitar", line = guitarLine, done = function(d) return practice(d, "guitar").doneToday end },
    { page = "reading", name = "Reading", line = readingLine, done = function(d) return practice(d, "reading").doneToday end },
    { page = "english", name = "English", line = englishLine, done = function(d)
        local t = englishPack(d)
        return t ~= nil and t.readDone == true and t.spokeDone == true
    end },
}

local function buttonGrid(c, data)
    for i, b in ipairs(BUTTONS) do
        local col, rw2 = (i - 1) % 2, 174
        local row = math.floor((i - 1) / 2)
        local bx, by = RX + col * 194, 444 + row * 122
        c:rect(bx, by, rw2, 106, "white")
        c:border(bx, by, rw2, 106, 3)
        c:hit(bx, by, rw2, 106, { type = "nav", page = b.page })
        c:text(bx + 14, by + 10, 26, b.name, { bold = true, maxw = rw2 - 62 })
        c:checkbox(bx + rw2 - 44, by + 12, 30, b.done(data) and true or false)
        c:box(bx + 14, by + 50, rw2 - 28, 50, 19, b.line(data), { font = "sans", color = "darkgray" })
    end
end

local function todayPanel(c, data, now)
    local x, y, w, h = RX, 692, RW, 356
    c:panel(x, y, w, h, "Today")
    c:hit(x, y, w, h, { type = "nav", page = "day", offset = 0 })
    local t = R.today(now)
    local left = {}
    for _, ev in ipairs(eventsOn(data, t)) do
        local e = R.parseISO(ev["end"] or ev.start)
        if not e or stamp(e) >= now then left[#left + 1] = ev end
    end
    if #left == 0 then
        local nx = nextEvent(data, now)
        if not nx then
            c:text(x + 16, y + 62, 28, "Nothing left today.", { color = "darkgray", maxw = w - 32 })
            return
        end
        local s = R.parseISO(nx.start)
        local sd = os.date("*t", stamp(s))
        c:text(x + 16, y + 58, 22, "Next: " .. DAYS3[sd.wday] .. " " .. s.day .. " " .. MONTHS3[s.month] .. "  " .. hm(s),
            { font = "sans", color = "darkgray", maxw = w - 32 })
        c:box(x + 16, y + 90, w - 32, 80, 28, nx.title or "", {})
        return
    end
    local ry, shown = y + 56, math.min(3, #left)
    for i = 1, shown do
        local ev = left[i]
        local s, e = R.parseISO(ev.start), R.parseISO(ev["end"])
        local when = s and hm(s) or ""
        if s and e and (s.hour ~= 0 or s.min ~= 0) then when = when .. " to " .. hm(e) end
        if ev.location and ev.location ~= "" then when = when .. "   " .. ev.location end
        c:text(x + 16, ry + 2, 20, when, { font = "sans", color = "darkgray", maxw = w - 32 })
        c:box(x + 16, ry + 26, w - 32, 54, 24, ev.title or "", {})
        if i < shown then c:rect(x + 16, ry + 84, w - 32, 2, "lightgray") end
        ry = ry + 88
    end
    if #left > shown then
        c:text(x + 16, ry + 4, 20, "and " .. (#left - shown) .. " more today", { font = "sans", color = "darkgray", maxw = w - 32 })
    end
end

local function newsPanel(c, data)
    local x, y, w, h = LX, 736, LW, 312
    local colw = math.floor(w / 2)
    c:rect(x, y, w, h, "white")
    c:border(x, y, w, h, 3)
    c:rect(x, y, w, 48, "black")
    c:text(x + 16, y + 11, 22, "HACKER NEWS", { color = "white", font = "sans", maxw = colw - 32 })
    c:text(x + colw + 16, y + 11, 22, "NEWS", { color = "white", font = "sans", maxw = colw - 32 })
    c:hit(x, y, w, h, { type = "nav", page = "news" })
    c:rect(x + colw, y + 48, 2, h - 48, "black")

    local ry, rows = y + 48, 3
    local al = alerts(data)
    if #al > 0 then
        local label = "SECURITY: " .. (al[1].title or "")
        if #al > 1 then label = "SECURITY (" .. #al .. "): " .. (al[1].title or "") end
        c:rect(x + 3, ry, w - 6, 52, "black")
        c:text(x + 20, ry + 13, 24, label, { color = "white", font = "sans", maxw = w - 40 })
        c:hit(x + 3, ry, w - 6, 52, { type = "nav", page = "security" })
        ry, rows = ry + 52, 2
    end

    local function column(items, cx)
        local yy = ry + 10
        if #items == 0 then
            c:text(cx + 16, yy, 24, "Nothing new.", { color = "darkgray", maxw = colw - 32 })
            return
        end
        for i = 1, math.min(rows, #items) do
            local n = items[i]
            c:box(cx + 16, yy, colw - 32, 58, 24, n.title or "", {})
            c:text(cx + 16, yy + 60, 20, n.source or "", { font = "sans", color = "darkgray", maxw = colw - 32 })
            yy = yy + 84
        end
    end
    column(data.hn or {}, x)
    column(data.news or {}, x + colw)
end

---------------------------------------------------------------- detail pages

local function topbarPage(c, state, title, right)
    c:rect(0, 80, R.W, 4, "black")
    c:button(24, 12, 150, "< Home", { type = "nav", page = "home" })
    c:text(200, 16, 40, title, { bold = true, maxw = 760 })
    if right and right ~= "" then c:text(1040, 28, 24, right, { font = "sans", align = "right", maxw = 300 }) end
    c:text(1258, 28, 24, syncText(state), { font = "sans", align = "right", maxw = 300 })
    c:button(1284, 12, 140, "Refresh", { type = "refresh" })
end

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
            if t.due and t.due ~= "" then meta = meta .. "  |  due " .. t.due end
            if t.nextStep and t.nextStep ~= "" then meta = meta .. "  |  Next: " .. t.nextStep end
            c:text(x + 96, y + 56, 24, meta, { font = "sans", color = "darkgray", maxw = w - 120 })
            y = y + 96
        end
        if #items == 0 then row(c, x, y, w, 60, "Nothing here. Good."); y = y + 60 end
        y = y + 24
    end
    list(top, "Top 3 today")
    local tc = data.taskCounts or {}
    list(rest, "Next" .. ((tc.backlog or 0) > 0 and ("   (" .. tc.backlog .. " more in the backlog)") or ""))
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

local function practiceFrame(c, state, title, keyName, done, doneLabel, last7, todoLabel, right)
    topbarPage(c, state, title, right)
    c:week(44, R.H - 128, last7)
    c:button(R.W - 244, R.H - 80, 220, done and doneLabel or (todoLabel or "Mark done"), { type = "toggle_practice", key = keyName, done = not done }, done)
end

local function pageGuitar(c, data, state)
    local g = practice(data, "guitar")
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

local function pick(list, n)
    if not list or #list == 0 then return nil end
    return list[(n % #list) + 1]
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
    local g = practice(data, "german")
    local s = pick(g.sentences, state.refreshCount or 0) or {}
    local items = {}
    for _, r in ipairs(g.review or {}) do items[#items + 1] = { r.de, r.en } end
    local co = g.course
    local title = "German" .. (g.level and (" " .. g.level) or "")
    if co and co.started then title = title .. "   day " .. (co.day or 0) end
    reviewPage(c, data, state, title, "german", g, s.de or "", s.en or "", "Reviews due", items)
    if co and co.started then
        c:text(24, R.H - 170, 22, germanLine(data), { font = "sans", color = "darkgray", maxw = 660 })
    end
end

local function pageVocab(c, data, state)
    local v = practice(data, "vocab")
    local w = pick(v.words, state.refreshCount or 0) or {}
    local items = {}
    for _, r in ipairs(v.review or {}) do items[#items + 1] = { r.word, r.definition } end
    reviewPage(c, data, state, "English vocabulary", "vocab", v, (w.word or "") .. (w.pos and ("   " .. w.pos) or ""),
        (w.definition or "") .. (w.example and ("\n" .. w.example) or ""), "Words due", items)
end

-- Reading is the book summary plan: one book a day, read the summary, mark it read.
local function pageReading(c, data, state)
    local r = practice(data, "reading")
    local s = r.summary or {}
    local today = s.today
    local sm = today and today.summary or nil
    local right = s.total and ((s.readCount or 0) .. " of " .. s.total .. " read") or nil
    practiceFrame(c, state, "Reading", "reading", r.doneToday, "Read today", r.last7, "Mark read", right)

    local x, w = 24, 700
    section(c, x, 104, w, "Today's book")
    if not today then
        c:box(x + 20, 168, w - 40, 120, 32, s.finished and "The plan is finished. Add more books on the dashboard."
            or "No book for today yet.", { color = "darkgray" })
    else
        c:box(x + 20, 162, w - 40, 104, 40, today.title or "", {})
        c:text(x + 20, 274, 28, today.author or "", { color = "darkgray", maxw = w - 40 })
        c:text(x + 20, 314, 22, string.upper(today.category or ""), { font = "sans", maxw = w - 40 })
        local one = sm and sm.intro and sm.intro.oneLine or nil
        if one and one ~= "" then c:box(x + 20, 350, w - 40, 76, 26, one, {}) end
        c:box(x + 20, 436, w - 40, 110, 24, today.why or "", { font = "sans", color = "darkgray" })
        local stats = {
            { tostring(s.day or 0), "day of " .. (s.total or 0) },
            { sm and tostring(sm.minutes or 0) or "-", "minutes to read" },
            { tostring(s.readCount or 0), "books read" },
        }
        for i, st in ipairs(stats) do
            local sx = x + 20 + (i - 1) * 230
            c:text(sx, 570, 56, st[1], { bold = true, font = "sans", maxw = 220 })
            c:text(sx, 636, 22, string.upper(st[2]), { font = "sans", maxw = 220 })
        end
        if today.status == "ready" and sm then
            c:button(x + 20, 690, 300, "Read the summary", { type = "nav", page = "summary" })
        else
            c:text(x + 20, 702, 24, today.status == "error" and "The summary failed to generate."
                or "The summary is not ready yet.", { font = "sans", color = "darkgray", maxw = w - 40 })
        end
    end

    local rx, rw, ry = 748, 676, 104
    if sm and #(sm.lessons or {}) > 0 then
        section(c, rx, ry, rw, "What is inside"); ry = ry + 44
        for i, l in ipairs(sm.lessons) do
            if i > 6 then break end
            c:text(rx + 20, ry + 8, 26, i .. ". " .. (l.title or ""), { maxw = rw - 40 })
            c:rect(rx + 20, ry + 46, rw - 40, 2, "lightgray")
            ry = ry + 50
        end
        local extra = {}
        if #(sm.insights or {}) > 0 then extra[#extra + 1] = #sm.insights .. " psychology notes" end
        if #(sm.exercises or {}) > 0 then extra[#extra + 1] = #sm.exercises .. " exercises" end
        if #(sm.takeaways or {}) > 0 then extra[#extra + 1] = #sm.takeaways .. " takeaways" end
        if #extra > 0 then
            c:text(rx + 20, ry + 10, 22, table.concat(extra, "   "), { font = "sans", color = "darkgray", maxw = rw - 40 })
            ry = ry + 42
        end
        ry = ry + 20
    end
    section(c, rx, ry, rw, "Up next"); ry = ry + 44
    local nx = s.next or r.next or {}
    if #nx == 0 then row(c, rx, ry, rw, 90, "Nothing queued.") end
    for i, nb in ipairs(nx) do
        if i > 4 or ry + 90 > R.H - 110 then break end
        row(c, rx, ry, rw, 90, nb.title or "", (nb.author or "") .. (nb.date and ("   " .. nb.date) or ""))
        ry = ry + 90
    end
end

-- The English daily pack: phrases, a speaking topic, a read-aloud passage.
local function pageEnglish(c, data, state)
    local e = practice(data, "english")
    local t = e.today
    local right = e.days7 and (e.days7 .. " of last 7 days") or nil
    topbarPage(c, state, "English", right)
    if not t or t.status ~= "ready" then
        row(c, 24, 104, 1400, 96, not t and "Nothing for today yet." or "Today's pack is not ready.",
            t and t.error or "It is generated every 30 minutes, and you can force it from the dashboard.")
        return
    end
    local x, y, w = 24, 104, 700
    section(c, x, y, w, "Phrases"); y = y + 44
    local ph = t.phrases or {}
    if #ph == 0 then row(c, x, y, w, 80, "No phrases today.") end
    for i, p in ipairs(ph) do
        if i > 8 or y + 96 > R.H - 100 then break end
        c:text(x + 20, y + 4, 28, p.phrase or "", { maxw = w - 40 })
        c:text(x + 20, y + 40, 21, p.meaning or "", { font = "sans", color = "darkgray", maxw = w - 40 })
        c:text(x + 20, y + 66, 21, (p.register and ("[" .. p.register .. "] ") or "") .. (p.example or ""),
            { font = "sans", color = "darkgray", maxw = w - 40 })
        c:rect(x, y + 93, w, 2, "lightgray")
        y = y + 96
    end

    local rx, ry, rw = 748, 104, 676
    local topic = t.topic or {}
    section(c, rx, ry, rw, "Speak for 2 to 3 minutes"); ry = ry + 44
    c:box(rx + 20, ry + 10, rw - 40, 84, 32, topic.title or "", {})
    c:box(rx + 20, ry + 100, rw - 40, 64, 22, topic.why or "", { font = "sans", color = "darkgray" })
    local ay = ry + 170
    for i, a in ipairs(topic.angles or {}) do
        if i > 3 then break end
        c:text(rx + 20, ay, 24, "\226\128\162 " .. a, { maxw = rw - 40 })
        ay = ay + 32
    end
    if topic.twist and topic.twist ~= "" then
        c:box(rx + 20, ay + 6, rw - 40, 56, 22, "End with: " .. topic.twist, { font = "sans", color = "darkgray" })
    end

    local py = ry + 310
    local pa = t.passage or {}
    section(c, rx, py, rw, "Read aloud"); py = py + 44
    c:box(rx + 20, py + 10, rw - 40, 80, 30, pa.title or "", {})
    c:text(rx + 20, py + 96, 22, (pa.wordCount or 0) .. " words, about " .. (pa.minutes or 0) .. " minutes",
        { font = "sans", color = "darkgray", maxw = rw - 40 })
    c:button(rx + 20, py + 130, 300, "Open the passage", { type = "nav", page = "passage" })
    local preview = tostring(pa.text or ""):gsub("%s+", " ")
    if #preview > 400 then preview = preview:sub(1, 400):gsub("%s%S*$", "") .. "..." end
    c:box(rx + 20, py + 200, rw - 40, 220, 24, preview, { color = "darkgray" })

    c:button(24, R.H - 80, 300, t.spokeDone and "Spoke today" or "Mark spoken",
        { type = "english_done", day = t.day, part = "speak", done = not t.spokeDone }, t.spokeDone)
    c:button(574, R.H - 80, 300, "Vocabulary", { type = "nav", page = "vocab" })
    c:button(R.W - 324, R.H - 80, 300, t.readDone and "Read today" or "Mark read aloud",
        { type = "english_done", day = t.day, part = "read", done = not t.readDone }, t.readDone)
end

local function pageForecast(c, data, state)
    topbarPage(c, state, "Weather")
    local list = data.forecast or {}
    if #list == 0 then
        row(c, 24, 104, 1400, 96, "No forecast yet.", "Add places on the dashboard's weather page.")
        return
    end
    local x, w, y = 24, 1400, 104
    for i, pf in ipairs(list) do
        if i > 5 then break end
        local p, cur = pf.place or {}, pf.current or {}
        c:text(x + 20, y + 10, 32, p.name or "", { bold = true, maxw = 380 })
        if pf.error and pf.error ~= "" then
            c:text(x + 20, y + 54, 22, pf.error, { font = "sans", color = "darkgray", maxw = 380 })
        else
            c:text(x + 20, y + 50, 40, string.format("%d\194\176", round(cur.tempC)), { bold = true, font = "sans", maxw = 180 })
            c:text(x + 20, y + 100, 22, cur.summary or "", { font = "sans", color = "darkgray", maxw = 380 })
            c:text(x + 20, y + 128, 20, "humidity " .. (cur.humidity or 0) .. "%   wind " .. round(cur.windKmh) .. " km/h",
                { font = "sans", color = "darkgray", maxw = 380 })
        end
        for j, d in ipairs(pf.days or {}) do
            if j > 7 then break end
            local dx = x + 420 + (j - 1) * 138
            local dt = R.parseISO(d.date)
            local label = dt and DAYS3[os.date("*t", stamp(dt)).wday] or ""
            if j == 1 then label = "Today" end
            c:text(dx + 66, y + 12, 22, label, { font = "sans", align = "center", maxw = 132 })
            c:text(dx + 66, y + 44, 34, string.format("%d\194\176", round(d.hiC)), { bold = true, align = "center", maxw = 132 })
            c:text(dx + 66, y + 88, 24, string.format("%d\194\176", round(d.loC)), { color = "darkgray", align = "center", maxw = 132 })
            c:text(dx + 66, y + 122, 20, (d.rainPct or 0) .. "%", { font = "sans", color = "darkgray", align = "center", maxw = 132 })
        end
        y = y + 170
        if i < math.min(5, #list) then c:rect(x, y - 16, w, 2, "lightgray") end
    end
end

local function newsColumn(c, items, x, y, w, limit)
    for i, n in ipairs(items) do
        if i > limit or y + 96 > R.H - 24 then break end
        c:text(x + 20, y + 8, 28, (n.alert and "\226\150\160 " or "") .. (n.title or ""), { maxw = w - 40 })
        c:text(x + 20, y + 46, 22, (n.source and (n.source .. "   ") or "") .. (n.summary or ""),
            { font = "sans", color = "darkgray", maxw = w - 40 })
        c:rect(x, y + 93, w, 2, "lightgray")
        y = y + 96
    end
    if #items == 0 then row(c, x, y, w, 96, "Nothing new.") end
end

local function pageNews(c, data, state)
    topbarPage(c, state, "News")
    local x, y, w = 24, 104, 690
    section(c, x, y, w, "Hacker News")
    section(c, x + 710, y, w, "News")
    newsColumn(c, data.hn or {}, x, y + 44, w, 9)
    newsColumn(c, data.news or {}, x + 710, y + 44, w, 9)
    local al = #alerts(data)
    c:button(R.W - 324, R.H - 80, 300, al > 0 and ("Security (" .. al .. " alerts)") or "Security",
        { type = "nav", page = "security" }, al > 0)
end

local function pageSecurity(c, data, state)
    local list = data.security or {}
    local al = alerts(data)
    topbarPage(c, state, "Security", #al > 0 and (#al .. " flagged") or "nothing flagged")
    local x, y, w = 24, 104, 1400
    if #list == 0 then
        row(c, x, y, w, 96, "Nothing from the security feeds.")
        return
    end
    -- flagged items first, they are the ones worth acting on
    local ordered = {}
    for _, n in ipairs(list) do if n.alert then ordered[#ordered + 1] = n end end
    for _, n in ipairs(list) do if not n.alert then ordered[#ordered + 1] = n end end
    for _, n in ipairs(ordered) do
        if y + 106 > R.H - 24 then break end
        if n.alert then c:rect(x, y, 8, 100, "black") end
        c:text(x + 28, y + 8, 30, n.title or "", { maxw = w - 56, bold = n.alert })
        c:box(x + 28, y + 48, w - 56, 52, 22, (n.source and (n.source .. "   ") or "") .. (n.summary or ""),
            { font = "sans", color = "darkgray" })
        c:rect(x, y + 103, w, 2, "lightgray")
        y = y + 106
    end
end

---------------------------------------------------------------- the reader

-- Long text (a book summary, a read-aloud passage) is paged by whole paragraphs. Pure Lua
-- cannot measure text, so the budget is in characters and deliberately low: a short page is
-- better than a page whose last lines are cut off.
local READER_X, READER_W, READER_SIZE = 254, 940, 28
local READER_BUDGET = 1350

local function paragraphs(text)
    local out = {}
    for raw in (tostring(text or "") .. "\n\n"):gmatch("(.-)\n%s*\n") do
        local part = raw:gsub("^%s+", ""):gsub("%s+$", "")
        if part ~= "" then out[#out + 1] = part end
    end
    if #out == 0 then
        local one = tostring(text or ""):gsub("^%s+", ""):gsub("%s+$", "")
        if one ~= "" then out[1] = one end
    end
    return out
end

-- packs paragraphs into page sized blocks, splitting an over long paragraph at a sentence end
local function blocks(text)
    local out, buf = {}, ""
    local function flush() if buf ~= "" then out[#out + 1] = buf; buf = "" end end
    for _, para in ipairs(paragraphs(text)) do
        local p = para
        while #p > READER_BUDGET do
            flush()
            local cut = p:sub(1, READER_BUDGET):match("^.*()%. ") or READER_BUDGET
            out[#out + 1] = p:sub(1, cut)
            p = p:sub(cut + 1):gsub("^%s+", "")
        end
        if buf == "" then buf = p
        elseif #buf + 2 + #p <= READER_BUDGET then buf = buf .. "\n\n" .. p
        else flush(); buf = p end
    end
    flush()
    return out
end

local function addSection(pages, heading, text)
    local bs = blocks(text)
    if #bs == 0 then
        if heading then pages[#pages + 1] = { heading = heading, body = "" } end
        return
    end
    for i, b in ipairs(bs) do
        pages[#pages + 1] = { heading = (i == 1) and heading or nil, body = b }
    end
end

local function joined(list, prefix)
    local body = ""
    for i, v in ipairs(list or {}) do
        body = body .. (i > 1 and "\n\n" or "") .. (prefix and (i .. ". ") or "") .. tostring(v)
    end
    return body
end

local function summaryPages(entry)
    local pages, s = {}, entry.summary or {}
    local intro = s.intro or {}
    addSection(pages, entry.title or "Summary",
        joined({ intro.oneLine, intro.authorAim, intro.aboutAuthor }))
    addSection(pages, "The big idea", intro.bigIdea or "")
    if intro.whoFor and intro.whoFor ~= "" then addSection(pages, "Why this one matters for you", intro.whoFor) end
    for i, l in ipairs(s.lessons or {}) do
        local body = l.idea or ""
        if l.atWork and l.atWork ~= "" then body = body .. "\n\nAt work: " .. l.atWork end
        if l.inLife and l.inLife ~= "" then body = body .. "\n\nIn life: " .. l.inLife end
        if l.remember and l.remember ~= "" then body = body .. "\n\nRemember: " .. l.remember end
        addSection(pages, "Lesson " .. i .. ": " .. (l.title or ""), body)
    end
    for _, ins in ipairs(s.insights or {}) do
        local body = ins.explanation or ""
        if ins.evidence and ins.evidence ~= "" then body = body .. "\n\nEvidence: " .. ins.evidence end
        if ins.source and ins.source ~= "" then body = body .. "\n\nSource: " .. ins.source end
        addSection(pages, "Psychology: " .. (ins.title or ""), body)
    end
    for _, ex in ipairs(s.exercises or {}) do
        local body = ex.why or ""
        local steps = joined(ex.steps, true)
        if steps ~= "" then body = body .. "\n\n" .. steps end
        if ex.when and ex.when ~= "" then body = body .. "\n\nWhen: " .. ex.when end
        addSection(pages, "Exercise: " .. (ex.title or ""), body)
    end
    if s.limits and s.limits ~= "" then addSection(pages, "Where the book is weak", s.limits) end
    if #(s.connections or {}) > 0 then addSection(pages, "Connections", joined(s.connections)) end
    if #(s.takeaways or {}) > 0 then
        local body = joined(s.takeaways, true)
        if s.finalThought and s.finalThought ~= "" then body = body .. "\n\n" .. s.finalThought end
        addSection(pages, "Takeaways", body)
    end
    return pages
end

local function passagePages(pa)
    local pages = {}
    addSection(pages, pa.title or "Read aloud", pa.text or "")
    local words = {}
    for _, w in ipairs(pa.words or {}) do words[#words + 1] = (w.word or "") .. ": " .. (w.meaning or "") end
    if #words > 0 then addSection(pages, "Words to notice", joined(words)) end
    if #(pa.tips or {}) > 0 then addSection(pages, "How to read it", joined(pa.tips)) end
    if #(pa.questions or {}) > 0 then addSection(pages, "Think about", joined(pa.questions, true)) end
    return pages
end

local function pageReader(c, data, state, kind)
    local pages, title, action, label, dark
    if kind == "summary" then
        local t = (practice(data, "reading").summary or {}).today
        if not t or not t.summary then
            topbarPage(c, state, "Summary")
            row(c, 24, 104, 1400, 96, "No summary for today yet.", "It is generated every 30 minutes.")
            return
        end
        title, pages = t.title or "Summary", summaryPages(t)
        local read = t.readAt ~= nil
        action, label, dark = { type = "summary_read", id = t.id, done = not read }, read and "Read" or "Mark read", read
    else
        local e = practice(data, "english").today
        local pa = e and e.passage or nil
        if not pa then
            topbarPage(c, state, "Read aloud")
            row(c, 24, 104, 1400, 96, "No passage for today yet.", "It is generated every 30 minutes.")
            return
        end
        title, pages = pa.title or "Read aloud", passagePages(pa)
        action = { type = "english_done", day = e.day, part = "read", done = not e.readDone }
        label, dark = e.readDone and "Read today" or "Mark read aloud", e.readDone
    end
    local n = math.max(1, #pages)
    local i = math.min(math.max(state.readerPage or 1, 1), n)
    local pg = pages[i] or { body = "" }
    topbarPage(c, state, title, i .. " of " .. n)
    local y = 116
    if pg.heading then
        c:box(READER_X, y, READER_W, 84, 34, pg.heading, { bold = true })
        y = y + 96
    end
    c:box(READER_X, y, READER_W, R.H - y - 100, READER_SIZE, pg.body, {})
    if i > 1 then c:button(24, R.H - 80, 220, "< Back", { type = "reader", delta = -1 }) end
    if i < n then c:button(R.W - 244, R.H - 80, 220, "Next >", { type = "reader", delta = 1 }) end
    c:button(574, R.H - 80, 300, label, action, dark)
end

---------------------------------------------------------------- entry

function R.render(data, state, now)
    now = now or os.time()
    data = data or {}
    local c = newCanvas()
    c:rect(0, 0, R.W, R.H, "white")
    local page = state.page or "home"
    if page == "home" then
        header(c, data, state, now)
        taskPanel(c, data)
        newsPanel(c, data)
        calendarPanel(c, data, now)
        buttonGrid(c, data)
        todayPanel(c, data, now)
        if state.error then c:text(28, 126, 20, state.error, { font = "sans", maxw = 700 }) end
    elseif page == "tasks" then pageTasks(c, data, state)
    elseif page == "day" then pageDay(c, data, state, now)
    elseif page == "guitar" then pageGuitar(c, data, state)
    elseif page == "german" then pageGerman(c, data, state)
    elseif page == "vocab" then pageVocab(c, data, state)
    elseif page == "reading" then pageReading(c, data, state)
    elseif page == "english" then pageEnglish(c, data, state)
    elseif page == "forecast" then pageForecast(c, data, state)
    elseif page == "summary" then pageReader(c, data, state, "summary")
    elseif page == "passage" then pageReader(c, data, state, "passage")
    elseif page == "news" then pageNews(c, data, state)
    elseif page == "security" then pageSecurity(c, data, state)
    else
        topbarPage(c, state, page)
        row(c, 24, 104, 1400, 96, "This page is not built yet.", "Tap < Home to go back.")
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
