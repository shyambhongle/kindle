-- v4: weather places (home + up to four more), managed in the web dashboard
CREATE TABLE IF NOT EXISTS weather_places (
  id        SERIAL PRIMARY KEY,
  name      TEXT NOT NULL,
  label     TEXT NOT NULL DEFAULT '',
  lat       DOUBLE PRECISION NOT NULL,
  lon       DOUBLE PRECISION NOT NULL,
  timezone  TEXT NOT NULL DEFAULT '',
  is_home   BOOLEAN NOT NULL DEFAULT FALSE,
  sort      INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS weather_places_one_home ON weather_places(is_home) WHERE is_home;
-- kv key added: forecast (json array of PlaceForecast)
