package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shyam/dashboard/internal/model"
)

const maxPlaces = 5 // home + four

type weatherData struct {
	Forecasts []model.PlaceForecast
	Places    []model.Place
	Status    string
	Query     string
	Results   []model.Place
	SearchErr string
	CanAdd    bool
	Today     string
}

func (a *Admin) weather(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := weatherData{Today: a.col.Today().Format("2006-01-02")}
	_ = a.col.GetKV(ctx, "forecast", &d.Forecasts)
	var st map[string]string
	if a.col.GetKV(ctx, "source_status", &st) == nil {
		d.Status = st["weather"]
	}
	d.Places, _ = a.col.Places(ctx)
	d.CanAdd = len(d.Places) < maxPlaces
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		d.Query = q
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		res, err := a.col.Geocode(sctx, q)
		if err != nil {
			d.SearchErr = "Search failed: " + err.Error()
		} else if len(res) == 0 {
			d.SearchErr = "No place found for that name. Try adding the country, or enter coordinates by hand."
		}
		d.Results = res
	}
	a.render(w, r, "weather", d)
}

func (a *Admin) weatherRefresh(w http.ResponseWriter, r *http.Request) {
	_, err := a.col.CollectWeather(r.Context())
	back(w, r, "/admin/weather", "Forecasts refreshed.", err)
}

func (a *Admin) placeCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	places, _ := a.col.Places(ctx)
	if len(places) >= maxPlaces {
		back(w, r, "/admin/weather", "", errors.New("you already follow 5 places; delete one first"))
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(r.FormValue("lat")), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(r.FormValue("lon")), 64)
	if name == "" || err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		back(w, r, "/admin/weather", "", errors.New("name, latitude and longitude are required"))
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		label = name
	}
	home := len(places) == 0
	_, err := a.pool.Exec(ctx, `INSERT INTO weather_places(name,label,lat,lon,timezone,is_home,sort) VALUES($1,$2,$3,$4,$5,$6,(SELECT coalesce(max(sort),0)+1 FROM weather_places))`,
		name, label, lat, lon, strings.TrimSpace(r.FormValue("timezone")), home)
	if err == nil {
		_, err = a.col.CollectWeather(ctx)
	}
	back(w, r, "/admin/weather", "Added "+name+".", err)
}

func (a *Admin) placeHome(w http.ResponseWriter, r *http.Request) {
	id := atoi(r.PathValue("id"))
	tx, err := a.pool.Begin(r.Context())
	if err != nil {
		back(w, r, "/admin/weather", "", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `UPDATE weather_places SET is_home=false WHERE is_home`); err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE weather_places SET is_home=true WHERE id=$1`, id)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err == nil {
		_, err = a.col.CollectWeather(r.Context())
	}
	back(w, r, "/admin/weather", "Home place changed.", err)
}

func (a *Admin) placeDelete(w http.ResponseWriter, r *http.Request) {
	id := atoi(r.PathValue("id"))
	var home bool
	_ = a.pool.QueryRow(r.Context(), `SELECT is_home FROM weather_places WHERE id=$1`, id).Scan(&home)
	if home {
		back(w, r, "/admin/weather", "", errors.New("make another place home before deleting this one"))
		return
	}
	_, err := a.pool.Exec(r.Context(), `DELETE FROM weather_places WHERE id=$1`, id)
	if err == nil {
		_, err = a.col.CollectWeather(r.Context())
	}
	back(w, r, "/admin/weather", "Place removed.", err)
}
