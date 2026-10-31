-- v10: Hacker News gets its own category so the Kindle can show it in its own column
-- next to the curated general feeds. It is still keyword filtered like a general feed.
UPDATE news_sources SET category = 'hn' WHERE url LIKE '%hnrss.org%' OR url LIKE '%news.ycombinator.com%';
UPDATE news_cache   SET category = 'hn'
  WHERE source IN (SELECT name FROM news_sources WHERE category = 'hn');
