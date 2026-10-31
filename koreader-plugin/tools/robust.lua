-- The Kindle must still draw when the backend is unreachable or the snapshot is half built.
package.path = (arg[0]:match("^(.*)/[^/]*$") or ".") .. "/../dashboard.koplugin/?.lua;" .. package.path
local R = require("dashboard_render")
local full = dofile((arg[0]:match("^(.*)/[^/]*$") or ".") .. "/mock.lua")

local cases = {
  { "empty table", {} },
  { "no practice", { tasks = {}, events = {} } },
  { "practice but no sub tables", { practice = {} } },
  { "german course not started", { practice = { german = { course = { started = false } } } } },
  { "english pack missing", { practice = { english = { generator = "live" } } } },
  { "summary not ready", { practice = { reading = { summary = { started = true, day = 3, total = 40,
      today = { id = 3, title = "A book", status = "error", error = "rate limited" } } } } } },
  { "forecast with only an error", { forecast = { { place = { name = "Pune", home = true }, error = "no result" } } } },
  { "security with no alerts", { security = { { id = "x", title = "Roundup", source = "Socket", alert = false } } } },
  { "full mock", full },
}
local pages = { "home", "tasks", "day", "guitar", "german", "vocab", "reading", "english", "forecast", "news", "security", "summary", "passage", "nonsense" }
local fails = 0
for _, case in ipairs(cases) do
  for _, pg in ipairs(pages) do
    for _, rp in ipairs({ 1, 3, 99 }) do
      local st = { page = pg, dayOffset = 0, refreshCount = 0, readerPage = rp, lastSync = nil, syncOk = false }
      local ok, err = pcall(R.render, case[2], st)
      if not ok then
        fails = fails + 1
        print(string.format("FAIL  %-28s page=%-9s readerPage=%d  %s", case[1], pg, rp, tostring(err)))
      end
    end
  end
end
-- taps must never return an action for a page that cannot handle it
local ops, hits = R.render({}, { page = "home" })
print(string.format("\nempty snapshot home: %d ops, %d hits", #ops, #hits))
print(fails == 0 and "All " .. (#cases * #pages * 3) .. " render combinations drew without error."
  or (fails .. " FAILURES"))
