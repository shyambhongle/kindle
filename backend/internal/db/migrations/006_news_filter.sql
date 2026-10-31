-- v6: curated general news. Feeds get a filter mode and a per-feed cap; seeds for AI, dev tooling and climate.
ALTER TABLE news_sources ADD COLUMN IF NOT EXISTS filter_mode TEXT NOT NULL DEFAULT 'keywords'; -- keywords | all
ALTER TABLE news_sources ADD COLUMN IF NOT EXISTS max_items   INT  NOT NULL DEFAULT 5;

INSERT INTO news_sources(url, name, category, filter_mode, max_items) VALUES
  ('https://github.blog/changelog/feed/',            'GitHub Changelog',     'general', 'all',      6),
  ('https://github.blog/feed/',                      'GitHub Blog',          'general', 'keywords', 4),
  ('https://hnrss.org/frontpage?points=150',         'Hacker News 150+',     'general', 'keywords', 8),
  ('https://openai.com/news/rss.xml',                'OpenAI',               'general', 'all',      4),
  ('https://deepmind.google/blog/rss.xml',           'Google DeepMind',      'general', 'all',      3),
  ('https://blog.google/technology/ai/rss/',         'Google AI',            'general', 'all',      3),
  ('https://huggingface.co/blog/feed.xml',           'Hugging Face',         'general', 'all',      3),
  ('https://simonwillison.net/atom/everything/',     'Simon Willison',       'general', 'keywords', 4),
  ('https://www.technologyreview.com/feed/',         'MIT Technology Review','general', 'keywords', 4),
  ('https://www.canarymedia.com/rss',                'Canary Media',         'general', 'all',      4),
  ('https://www.carbonbrief.org/feed/',              'Carbon Brief',         'general', 'keywords', 3),
  ('https://news.mongabay.com/feed/',                'Mongabay',             'general', 'keywords', 3)
ON CONFLICT (url) DO NOTHING;

-- local general news does not fit the brief; kept but switched off so nothing is lost
UPDATE news_sources SET enabled = false WHERE url = 'https://www.thehindu.com/news/cities/pune/feeder/default.rss';
-- security feeds are never keyword-filtered
UPDATE news_sources SET filter_mode = 'all', max_items = 15 WHERE category = 'security';
