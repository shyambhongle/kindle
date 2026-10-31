package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/shyam/dashboard/internal/model"
)

// Weather uses Open-Meteo: free, no API key. Forecast and geocoding endpoints.
type Weather struct {
	BaseURL    string // https://api.open-meteo.com
	GeocodeURL string // https://geocoding-api.open-meteo.com
	TZ         *time.Location
}

func NewWeather(tz *time.Location) *Weather {
	return &Weather{BaseURL: "https://api.open-meteo.com", GeocodeURL: "https://geocoding-api.open-meteo.com", TZ: tz}
}

// Forecast returns the current conditions and a 7 day forecast for one place.
func (w *Weather) Forecast(ctx context.Context, p model.Place) (model.PlaceForecast, error) {
	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.4f", p.Lat))
	q.Set("longitude", fmt.Sprintf("%.4f", p.Lon))
	q.Set("current", "temperature_2m,weather_code,relative_humidity_2m,wind_speed_10m")
	q.Set("daily", "temperature_2m_max,temperature_2m_min,weather_code,precipitation_probability_max,sunrise,sunset")
	q.Set("timezone", "auto")
	q.Set("forecast_days", "7")
	req, _ := http.NewRequestWithContext(ctx, "GET", w.BaseURL+"/v1/forecast?"+q.Encode(), nil)
	resp, err := Client.Do(req)
	if err != nil {
		return model.PlaceForecast{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return model.PlaceForecast{}, fmt.Errorf("open-meteo HTTP %d", resp.StatusCode)
	}
	var r struct {
		Current struct {
			Temp     float64 `json:"temperature_2m"`
			Code     int     `json:"weather_code"`
			Humidity float64 `json:"relative_humidity_2m"`
			Wind     float64 `json:"wind_speed_10m"`
		} `json:"current"`
		Daily struct {
			Time    []string  `json:"time"`
			Max     []float64 `json:"temperature_2m_max"`
			Min     []float64 `json:"temperature_2m_min"`
			Code    []int     `json:"weather_code"`
			Rain    []int     `json:"precipitation_probability_max"`
			Sunrise []string  `json:"sunrise"`
			Sunset  []string  `json:"sunset"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&r); err != nil {
		return model.PlaceForecast{}, err
	}
	out := model.PlaceForecast{Place: p, FetchedAt: time.Now(), Current: model.Current{
		TempC: r.Current.Temp, Code: r.Current.Code, Summary: WMOText(r.Current.Code), Humidity: int(r.Current.Humidity), WindKmh: r.Current.Wind,
	}}
	for i, day := range r.Daily.Time {
		d := model.ForecastDay{Date: day}
		if i < len(r.Daily.Max) {
			d.HiC = r.Daily.Max[i]
		}
		if i < len(r.Daily.Min) {
			d.LoC = r.Daily.Min[i]
		}
		if i < len(r.Daily.Code) {
			d.Code = r.Daily.Code[i]
			d.Summary = WMOText(d.Code)
		}
		if i < len(r.Daily.Rain) {
			d.RainPct = r.Daily.Rain[i]
		}
		if i < len(r.Daily.Sunrise) && len(r.Daily.Sunrise[i]) >= 16 {
			d.Sunrise = r.Daily.Sunrise[i][11:16]
		}
		if i < len(r.Daily.Sunset) && len(r.Daily.Sunset[i]) >= 16 {
			d.Sunset = r.Daily.Sunset[i][11:16]
		}
		out.Days = append(out.Days, d)
	}
	return out, nil
}

// Geocode searches place names (city, town, village) and returns candidates with coordinates.
func (w *Weather) Geocode(ctx context.Context, name string) ([]model.Place, error) {
	q := url.Values{}
	q.Set("name", name)
	q.Set("count", "6")
	q.Set("language", "en")
	q.Set("format", "json")
	req, _ := http.NewRequestWithContext(ctx, "GET", w.GeocodeURL+"/v1/search?"+q.Encode(), nil)
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("geocoding HTTP %d", resp.StatusCode)
	}
	var r struct {
		Results []struct {
			Name    string  `json:"name"`
			Lat     float64 `json:"latitude"`
			Lon     float64 `json:"longitude"`
			Country string  `json:"country"`
			Admin1  string  `json:"admin1"`
			TZ      string  `json:"timezone"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&r); err != nil {
		return nil, err
	}
	var out []model.Place
	for _, g := range r.Results {
		label := g.Name
		if g.Admin1 != "" && g.Admin1 != g.Name {
			label += ", " + g.Admin1
		}
		if g.Country != "" {
			label += ", " + g.Country
		}
		out = append(out, model.Place{Name: g.Name, Label: label, Lat: g.Lat, Lon: g.Lon, Timezone: g.TZ})
	}
	return out, nil
}

// WMOText maps WMO weather codes to short text, kept simple for a small screen.
func WMOText(code int) string {
	switch {
	case code == 0:
		return "Clear"
	case code <= 2:
		return "Partly cloudy"
	case code == 3:
		return "Overcast"
	case code <= 48:
		return "Fog"
	case code <= 57:
		return "Drizzle"
	case code <= 67:
		return "Rain"
	case code <= 77:
		return "Snow"
	case code <= 82:
		return "Showers"
	case code <= 86:
		return "Snow showers"
	default:
		return "Thunderstorm"
	}
}

// DummyForecast gives a believable week for a place when Open-Meteo is unreachable.
func DummyForecast(p model.Place, tz *time.Location) model.PlaceForecast {
	codes := []int{1, 2, 3, 61, 80, 1, 0}
	out := model.PlaceForecast{Place: p, FetchedAt: time.Now(), Current: model.Current{TempC: 27, Code: 1, Summary: "Partly cloudy", Humidity: 60, WindKmh: 9}}
	now := time.Now().In(tz)
	for i := 0; i < 7; i++ {
		d := now.AddDate(0, 0, i)
		out.Days = append(out.Days, model.ForecastDay{Date: d.Format("2006-01-02"), HiC: 31 - float64(i%3), LoC: 21 + float64(i%2), Code: codes[i], Summary: WMOText(codes[i]), RainPct: []int{10, 20, 40, 80, 60, 10, 0}[i], Sunrise: "06:32", Sunset: "18:05"})
	}
	return out
}
