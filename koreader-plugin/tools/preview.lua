-- Renders each page to SVG so the layout can be checked off-device, and reports
-- anything drawn outside the 1448 x 1072 canvas.
package.path = (arg[0]:match("^(.*)/[^/]*$") or ".") .. "/../dashboard.koplugin/?.lua;" .. package.path
local R = require("dashboard_render")
local DATA = dofile((arg[0]:match("^(.*)/[^/]*$") or ".") .. "/mock.lua")

local COL = { black = "#000000", white = "#ffffff", gray = "#808080", darkgray = "#404040", lightgray = "#cccccc" }
local function esc(s)
  return (tostring(s):gsub("&", "&amp;"):gsub("<", "&lt;"):gsub(">", "&gt;"))
end
local function fam(f) return f == "sans" and "Helvetica, Arial, sans-serif" or "Georgia, 'Times New Roman', serif" end

-- rough wrap for the preview only; the device uses TextBoxWidget
local function wrap(text, width, size)
  local per = math.max(4, math.floor(width / (size * 0.5)))
  local lines, line = {}, ""
  for _, word in ipairs((function() local t = {} for w in tostring(text):gmatch("%S+") do t[#t+1] = w end return t end)()) do
    if line == "" then line = word
    elseif #line + 1 + #word <= per then line = line .. " " .. word
    else lines[#lines+1] = line; line = word end
  end
  if line ~= "" then lines[#lines+1] = line end
  return lines
end

local function svg(page, state)
  local ops, hits = R.render(DATA, state)
  local out = { string.format('<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">', R.W, R.H, R.W, R.H) }
  out[#out+1] = string.format('<rect width="%d" height="%d" fill="#fff"/>', R.W, R.H)
  local bad = {}
  local function check(x, y, w, h, what)
    if x < 0 or y < 0 or x + (w or 0) > R.W or y + (h or 0) > R.H then
      bad[#bad+1] = string.format("%s out of canvas: x=%.0f y=%.0f w=%.0f h=%.0f (%s)", page, x, y, w or 0, h or 0, what)
    end
  end
  for _, o in ipairs(ops) do
    local c = COL[o.color] or "#000"
    if o.t == "rect" then
      check(o.x, o.y, o.w, o.h, "rect")
      out[#out+1] = string.format('<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>', o.x, o.y, o.w, o.h, c)
    elseif o.t == "border" then
      check(o.x, o.y, o.w, o.h, "border")
      out[#out+1] = string.format('<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="none" stroke="%s" stroke-width="%d"/>', o.x + o.bw/2, o.y + o.bw/2, o.w - o.bw, o.h - o.bw, c, o.bw)
    elseif o.t == "text" then
      check(o.x, o.y, 0, o.size, "text: " .. tostring(o.text):sub(1, 30))
      local anchor = o.align == "center" and "middle" or (o.align == "right" and "end" or "start")
      out[#out+1] = string.format('<text x="%.1f" y="%.1f" font-family="%s" font-size="%d" font-weight="%s" fill="%s" text-anchor="%s" xml:space="preserve">%s</text>',
        o.x, o.y + o.size * 0.82, fam(o.font), o.size, o.bold and "bold" or "normal", c, anchor, esc(o.text))
    elseif o.t == "box" then
      check(o.x, o.y, o.w, o.h, "box")
      local lines = wrap(o.text, o.w, o.size)
      local lh = o.size * 1.22
      for i, l in ipairs(lines) do
        if (i - 1) * lh + o.size > o.h then break end
        out[#out+1] = string.format('<text x="%.1f" y="%.1f" font-family="%s" font-size="%d" font-weight="%s" fill="%s" xml:space="preserve">%s</text>',
          o.x, o.y + (i - 1) * lh + o.size * 0.82, fam(o.font), o.size, o.bold and "bold" or "normal", c, esc(l))
      end
    end
  end
  for _, h in ipairs(hits) do check(h.x, h.y, h.w, h.h, "hit " .. tostring(h.action and h.action.type)) end
  out[#out+1] = '</svg>'
  return table.concat(out, "\n"), bad, #ops, #hits
end

local pages = { "home", "tasks", "day", "guitar", "german", "vocab", "reading", "english", "forecast", "news", "security",
  { "summary", 1 }, { "summary", 2 }, { "summary", 7 }, { "passage", 1 }, { "passage", 2 } }
local allBad = {}
for _, spec in ipairs(pages) do
  local p, rp = spec, 1
  if type(spec) == "table" then p, rp = spec[1], spec[2] end
  local name = type(spec) == "table" and (p .. "-" .. rp) or p
  local s, bad, nops, nhits = svg(name, { page = p, dayOffset = 0, refreshCount = 0, lastSync = "09:14", syncOk = true, readerPage = rp })
  local f = io.open((arg[1] or ".") .. "/page-" .. name .. ".svg", "w"); f:write(s); f:close()
  print(string.format("%-12s %4d ops %3d hits %s", name, nops, nhits, #bad == 0 and "ok" or (#bad .. " PROBLEMS")))
  for _, b in ipairs(bad) do allBad[#allBad+1] = b end
end
print("")
if #allBad == 0 then print("Nothing drawn outside the canvas.") else
  for _, b in ipairs(allBad) do print("  " .. b) end
end
