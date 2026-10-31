package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/shyam/dashboard/internal/model"
)

// Weather uses Open-Meteo: free, no API key.
type Weather struct {
	Lat, Lon float64
	Location string
}

func (w *Weather) Today(ctx context.Context) (model.Weather, error) {
	url := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,weather_code&daily=temperature_2m_max,temperature_2m_min&timezone=auto&forecast_days=1", w.Lat, w.Lon)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := Client.Do(req)
	if err != nil {
		return model.Weather{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return model.Weather{}, fmt.Errorf("open-meteo HTTP %d", resp.StatusCode)
	}
	var r struct {
		Current struct {
			Temp float64 `json:"temperature_2m"`
			Code int     `json:"weather_code"`
		} `json:"current"`
		Daily struct {
			Max []float64 `json:"temperature_2m_max"`
			Min []float64 `json:"temperature_2m_min"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&r); err != nil {
		return model.Weather{}, err
	}
	out := model.Weather{TempC: r.Current.Temp, Summary: wmoText(r.Current.Code), Location: w.Location}
	if len(r.Daily.Max) > 0 {
		out.HiC = r.Daily.Max[0]
	}
	if len(r.Daily.Min) > 0 {
		out.LoC = r.Daily.Min[0]
	}
	return out, nil
}

// WMO weather codes to short text, kept simple for a small screen.
func wmoText(code int) string {
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

func DummyWeather(location string) model.Weather {
	return model.Weather{TempC: 27, HiC: 31, LoC: 21, Summary: "Partly cloudy", Location: location}
}
