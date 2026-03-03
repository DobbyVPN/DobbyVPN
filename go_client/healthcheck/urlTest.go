package healthcheck

import (
	"net/http"

	"github.com/matsuridayo/libneko/speedtest"
)

const (
	urlTestTimeoutMilliseconds = 1000
)

var httpClient = &http.Client{}

func URLTest(url string, standard int) (int32, error) {
	return speedtest.UrlTest(httpClient, url, urlTestTimeoutMilliseconds, standard)
}
