--[[
Dashboard plugin for KOReader.

Shows a full screen, landscape dashboard drawn from the JSON your backend serves at
  <backend url>/d/<token>/api/dashboard
Taps mark tasks and practice done (POST), open detail pages, or refresh.

All layout lives in dashboard_render.lua (pure Lua). This file is the KOReader glue:
menu entry, settings, HTTP, rotation, painting the draw ops, and touch handling.
--]]

local Blitbuffer = require("ffi/blitbuffer")
local Device = require("device")
local Event = require("ui/event")
local Font = require("ui/font")
local Geom = require("ui/geometry")
local GestureRange = require("ui/gesturerange")
local InfoMessage = require("ui/widget/infomessage")
local InputContainer = require("ui/widget/container/inputcontainer")
local InputDialog = require("ui/widget/inputdialog")
local TextBoxWidget = require("ui/widget/textboxwidget")
local TextWidget = require("ui/widget/textwidget")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local logger = require("logger")
local _ = require("gettext")
local Screen = Device.screen

local R = require("dashboard_render")

local SETTING_URL = "dashboard_url"          -- e.g. http://192.168.1.20:8080/d/<token>/api
local SETTING_ROTATE = "dashboard_rotate"    -- true: force landscape while open

---------------------------------------------------------------- JSON and HTTP

local JSON
do
    local ok, mod = pcall(require, "rapidjson")
    if ok and mod and mod.decode then
        JSON = mod
    else
        JSON = require("json")
    end
end

local function httpJSON(method, url, body)
    local http = require("socket.http")
    local ltn12 = require("ltn12")
    local ok_su, socketutil = pcall(require, "socketutil")
    if ok_su and socketutil and socketutil.set_timeout then
        socketutil:set_timeout(10, 30)
    else
        http.TIMEOUT = 15
    end
    local chunks = {}
    local req = { url = url, method = method, sink = ltn12.sink.table(chunks), headers = { ["Accept"] = "application/json" } }
    if body then
        req.headers["Content-Type"] = "application/json"
        req.headers["Content-Length"] = tostring(#body)
        req.source = ltn12.source.string(body)
    end
    local ok, res, code = pcall(http.request, req)
    if ok_su and socketutil and socketutil.reset_timeout then socketutil:reset_timeout() end
    if not ok then return nil, tostring(res) end
    if not res then return nil, tostring(code) end          -- (nil, err) form
    if type(code) ~= "number" or code < 200 or code >= 300 then
        return nil, "HTTP " .. tostring(code)
    end
    local text = table.concat(chunks)
    local okd, data = pcall(JSON.decode, text)
    if not okd or type(data) ~= "table" then return nil, "bad JSON from backend" end
    return data
end

---------------------------------------------------------------- the view widget

local DashboardView = InputContainer:extend{
    name = "dashboard_view",
    covers_fullscreen = true,
    data = nil,       -- last dashboard JSON
    state = nil,      -- {page, dayOffset, refreshCount, lastSync, syncOk, error, showNews}
    url = nil,        -- api base url
    hits = {},
    scale = 1,
    offx = 0,
    offy = 0,
    on_close = nil,
}

local COLORS = {
    black = Blitbuffer.COLOR_BLACK,
    white = Blitbuffer.COLOR_WHITE,
    gray = Blitbuffer.gray(0.5),
    darkgray = Blitbuffer.gray(0.25),
    lightgray = Blitbuffer.gray(0.8),
}

function DashboardView:init()
    self.dimen = Geom:new{ x = 0, y = 0, w = Screen:getWidth(), h = Screen:getHeight() }
    self.scale = math.min(self.dimen.w / R.W, self.dimen.h / R.H)
    self.offx = math.floor((self.dimen.w - R.W * self.scale) / 2)
    self.offy = math.floor((self.dimen.h - R.H * self.scale) / 2)
    -- font sizes: KOReader scales point sizes by screen DPI, we want pixels, so undo that
    self.px = 1000 / Screen:scaleBySize(1000)
    self.faces = {}
    self.ges_events = {
        TapView = { GestureRange:new{ ges = "tap", range = self.dimen } },
        HoldView = { GestureRange:new{ ges = "hold", range = self.dimen } },
    }
    self.state = self.state or { page = "home", dayOffset = 0, refreshCount = 0, readerPage = 1 }
end

function DashboardView:face(kind, bold, sizePx)
    local size = math.max(6, math.floor(sizePx * self.scale * self.px + 0.5))
    local key = kind .. (bold and "b" or "") .. size
    if not self.faces[key] then
        local face
        if kind == "serif" then
            -- NotoSerif ships with KOReader; fall back to the UI sans font if it is missing
            local ok, f = pcall(Font.getFace, Font, "NotoSerif-Regular.ttf", size)
            if ok and f then face = f end
        end
        self.faces[key] = face or Font:getFace("cfont", size)
    end
    return self.faces[key]
end

function DashboardView:S(v) return math.floor(v * self.scale + 0.5) end

function DashboardView:paintTo(bb, x, y)
    local ops, hits = R.render(self.data or {}, self.state)
    self.hits = hits
    local ox, oy = x + self.offx, y + self.offy
    bb:paintRect(x, y, self.dimen.w, self.dimen.h, COLORS.white)
    for _, op in ipairs(ops) do
        local color = COLORS[op.color] or COLORS.black
        if op.t == "rect" then
            bb:paintRect(ox + self:S(op.x), oy + self:S(op.y), self:S(op.w), self:S(op.h), color)
        elseif op.t == "border" then
            bb:paintBorder(ox + self:S(op.x), oy + self:S(op.y), self:S(op.w), self:S(op.h), math.max(1, self:S(op.bw)), color)
        elseif op.t == "text" then
            local w = TextWidget:new{
                text = op.text, face = self:face(op.font, op.bold, op.size), bold = op.bold, fgcolor = color,
                padding = 0, max_width = op.maxw and self:S(op.maxw) or nil, truncate_with_ellipsis = true,
            }
            local tw = w:getSize().w
            local tx = ox + self:S(op.x)
            if op.align == "center" then tx = tx - math.floor(tw / 2) elseif op.align == "right" then tx = tx - tw end
            w:paintTo(bb, tx, oy + self:S(op.y))
            if w.free then w:free() end
        elseif op.t == "box" then
            local w = TextBoxWidget:new{
                text = op.text, face = self:face(op.font, op.bold, op.size), bold = op.bold, fgcolor = color,
                width = self:S(op.w), height = self:S(op.h), alignment = "left",
            }
            w:paintTo(bb, ox + self:S(op.x), oy + self:S(op.y))
            if w.free then w:free() end
        end
    end
end

function DashboardView:refreshScreen(kind)
    UIManager:setDirty(self, kind or "full")
end

function DashboardView:onTapView(_, ges)
    local cx = (ges.pos.x - self.dimen.x - self.offx) / self.scale
    local cy = (ges.pos.y - self.dimen.y - self.offy) / self.scale
    local action = R.hitTest(self.hits, cx, cy)
    if not action then return true end
    self:doAction(action)
    return true
end

function DashboardView:onHoldView()
    -- long press anywhere: back to home, or exit from home
    if self.state.page ~= "home" then
        self.state.readerPage = 1
        self.state.page = "home"
        self:refreshScreen("full")
    else
        self:close()
    end
    return true
end

function DashboardView:doAction(a)
    if a.type == "close" then
        self:close()
    elseif a.type == "nav" then
        self.state.page = a.page
        if a.offset then self.state.dayOffset = a.offset end
        self.state.readerPage = 1
        self:refreshScreen("full")
    elseif a.type == "reader" then
        -- long text: a full refresh per page turn, the same as turning a page in a book
        self.state.readerPage = math.max(1, (self.state.readerPage or 1) + (a.delta or 1))
        self:refreshScreen("full")
    elseif a.type == "day" then
        self.state.dayOffset = (self.state.dayOffset or 0) + a.delta
        self:refreshScreen("ui")
    elseif a.type == "refresh" then
        self.state.refreshCount = (self.state.refreshCount or 0) + 1
        self:fetch(true)
    elseif a.type == "toggle_task" then
        self:post("/tasks/" .. a.id .. "/done", { done = a.done }, function(t)
            -- optimistic local update if offline
            for _, task in ipairs((self.data or {}).tasks or {}) do if task.id == a.id then task.done = a.done end end
        end)
    elseif a.type == "toggle_practice" then
        self:post("/practice/" .. a.key .. "/done", { done = a.done }, function()
            local p = ((self.data or {}).practice or {})[a.key]
            if p then p.doneToday = a.done; if p.last7 and #p.last7 == 7 then p.last7[7] = a.done and 1 or 0 end end
        end)
    elseif a.type == "guitar_advance" then
        self:post("/practice/guitar/advance", { delta = a.delta or 1 }, nil)
    elseif a.type == "english_done" then
        self:post("/english/" .. tostring(a.day) .. "/done", { part = a.part, done = a.done }, nil)
    elseif a.type == "summary_read" then
        self:post("/summaries/" .. tostring(a.id) .. "/read", { done = a.done }, nil)
    end
end

function DashboardView:post(path, body, fallback)
    if not self.url then
        if fallback then fallback() end
        self:refreshScreen("ui")
        return
    end
    local data, err = httpJSON("POST", self.url .. path, JSON.encode(body))
    if data then
        self.data = data
        self.state.syncOk = true
        self.state.error = nil
    else
        logger.warn("dashboard: POST", path, err)
        if fallback then fallback() end
        self.state.error = "Write failed: " .. tostring(err)
    end
    self.state.lastSync = os.date("%H:%M")
    self:refreshScreen("ui")
end

function DashboardView:fetch(showMessage)
    if not self.url then
        self.state.error = "No backend URL set. Menu > Tools > Dashboard > Set backend URL."
        self:refreshScreen("full")
        return
    end
    local msg
    if showMessage then
        msg = InfoMessage:new{ text = _("Refreshing dashboard..."), timeout = 30 }
        UIManager:show(msg)
    end
    -- give the message a chance to paint before the blocking request
    UIManager:scheduleIn(0.1, function()
        local data, err = httpJSON("GET", self.url .. "/dashboard?t=" .. os.time())
        if msg then UIManager:close(msg) end
        if data then
            self.data = data
            self.state.syncOk = true
            self.state.error = nil
        else
            logger.warn("dashboard: GET", err)
            self.state.syncOk = false
            self.state.error = "Could not reach backend: " .. tostring(err)
        end
        self.state.lastSync = os.date("%H:%M")
        self:refreshScreen("full")
    end)
end

function DashboardView:close()
    UIManager:close(self)
    if self.on_close then self.on_close() end
end

function DashboardView:onCloseWidget()
    -- repaint whatever is underneath after we leave
    UIManager:setDirty("all", "full")
end

---------------------------------------------------------------- the plugin

local Dashboard = WidgetContainer:extend{
    name = "dashboard",
    is_doc_only = false,
}

function Dashboard:init()
    self.ui.menu:registerToMainMenu(self)
end

function Dashboard:addToMainMenu(menu_items)
    menu_items.dashboard = {
        text = _("Dashboard"),
        sorting_hint = "tools",
        sub_item_table = {
            {
                text = _("Open dashboard"),
                callback = function() self:open() end,
            },
            {
                text = _("Set backend URL"),
                keep_menu_open = true,
                callback = function() self:editURL() end,
            },
            {
                text = _("Rotate to landscape while open"),
                checked_func = function() return G_reader_settings:nilOrTrue(SETTING_ROTATE) end,
                callback = function() G_reader_settings:flipNilOrTrue(SETTING_ROTATE) end,
            },
        },
    }
end

function Dashboard:editURL()
    local current = G_reader_settings:readSetting(SETTING_URL) or "http://192.168.1.20:8080/d/<token>/api"
    local dialog
    dialog = InputDialog:new{
        title = _("Backend API URL"),
        description = _("The URL the backend prints at start, ending in /api (no trailing slash)."),
        input = current,
        input_type = "text",
        buttons = {{
            { text = _("Cancel"), callback = function() UIManager:close(dialog) end },
            {
                text = _("Save"), is_enter_default = true,
                callback = function()
                    local v = dialog:getInputText():gsub("%s+$", ""):gsub("/+$", "")
                    G_reader_settings:saveSetting(SETTING_URL, v)
                    UIManager:close(dialog)
                end,
            },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function Dashboard:open()
    local url = G_reader_settings:readSetting(SETTING_URL)
    local rotate = G_reader_settings:nilOrTrue(SETTING_ROTATE)
    local old_mode = Screen:getRotationMode()
    local landscape_mode = Screen.DEVICE_ROTATED_CLOCKWISE or 1

    local function show()
        if rotate and Screen:getWidth() < Screen:getHeight() then
            Screen:setRotationMode(landscape_mode)
        end
        local view = DashboardView:new{
            url = url,
            state = { page = "home", dayOffset = 0, refreshCount = 0, readerPage = 1 },
        }
        view.on_close = function()
            if rotate and Screen:getRotationMode() ~= old_mode then
                -- the reader and the file manager both handle this event and re-layout themselves
                local ok = pcall(UIManager.broadcastEvent, UIManager, Event:new("SetRotationMode", old_mode))
                if not ok or Screen:getRotationMode() ~= old_mode then
                    Screen:setRotationMode(old_mode)
                end
            end
            UIManager:setDirty("all", "full")
        end
        UIManager:show(view)
        view:refreshScreen("full")
        view:fetch(true)
    end

    local NetworkMgr = require("ui/network/manager")
    if NetworkMgr:isConnected() then
        show()
    elseif NetworkMgr.runWhenOnline then
        NetworkMgr:runWhenOnline(show)
    else
        NetworkMgr:promptWifiOn(show)
    end
end

return Dashboard
