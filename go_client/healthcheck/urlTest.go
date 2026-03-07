package healthcheck

import (
	"net/http"

	"github.com/matsuridayo/libneko/speedtest"
)

const (
	urlTestTimeoutMilliseconds = 1000
)

var httpClient = newHealthcheckHTTPClient()

func newHealthcheckHTTPClient() *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok || base == nil {
		return &http.Client{Transport: &http.Transport{}}
	}
	// Keep a dedicated clone so speedtest can tweak transport flags safely.
	return &http.Client{Transport: base.Clone()}
}

func URLTest(url string, standard int) (int32, error) {
	return speedtest.UrlTest(httpClient, url, urlTestTimeoutMilliseconds, standard)
}
