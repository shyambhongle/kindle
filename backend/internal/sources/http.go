// Package sources fetches external data. Every source has a Dummy() so the backend works with nothing configured.
package sources

import (
	"net/http"
	"time"
)

// Client is shared by all sources: short timeout, no redirects to odd places, small responses.
var Client = &http.Client{
	Timeout: 12 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

const maxBody = 4 << 20 // 4 MB is plenty for any feed, ICS or API reply
